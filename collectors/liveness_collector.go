// Copyright 2021 Akamai Technologies, Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package collectors

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v13/pkg/session"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

var durationBuckets = []float64{60, 1800, 3600, 7200, 14400}

const (
	livenessCollectorName = "liveness"
	// realTime replicas lag up to ~7h and serve rows late and out of order,
	// so the days overlapping this horizon are refetched and deduplicated.
	livenessLateness = 8 * time.Hour
)

type LivenessTMeta struct {
	URI      string `json:"uri"`
	Domain   string `json:"domain"`
	Property string `json:"property"`
	Date     string `json:"date"`
}

type LivenessDRow struct {
	Nickname          string `json:"nickname"`
	DatacenterID      int    `json:"datacenterId"`
	TrafficTargetName string `json:"trafficTargetName"`
	ErrorCode         int64  `json:"errorCode"`
	Duration          int64  `json:"duration"`
	TestName          string `json:"testName"`
	AgentIP           string `json:"agentIp"`
	TargetIP          string `json:"targetIp"`
	Status            string `json:"status"` // Added: Often present in GTM reports
}

type GTMLivenessTrafficExporter struct {
	GTMConfig                GTMMetricsConfig
	LivenessMetricPrefix     string
	LivenessLookbackDuration time.Duration
	LivenessRegistry         *prometheus.Registry
	api                      *APIPool
	opts                     PollOptions
	cache                    *sampleCache
	targets                  []*livenessTarget
	now                      func() time.Time

	// Per datacenter histograms and summaries, created on first error.
	mu         sync.Mutex
	histograms map[string]prometheus.Histogram
	summaries  map[string]prometheus.Summary
}

// livenessTarget is only touched by the worker polling it.
type livenessTarget struct {
	domain         string
	cfg            *LivenessTestConfig
	since          time.Time                  // rows up to this time are ignored
	seen           map[livenessEvent]struct{} // errors already processed, within the fetched days
	fetchedThrough time.Time                  // report window end at the last complete fetch
}

// livenessEvent identifies one datacenter error of a report row.
type livenessEvent struct {
	ts                        time.Time
	datacenter                int
	agentIP, targetIP, target string
	testName                  string
	errorCode                 int64
}

func NewLivenessTrafficCollector(api *APIPool, r *prometheus.Registry, gtmMetricsConfig GTMMetricsConfig, gtmMetricPrefix string, tstart time.Time, lookbackDuration time.Duration, opts PollOptions) *GTMLivenessTrafficExporter {
	l := &GTMLivenessTrafficExporter{
		GTMConfig:                gtmMetricsConfig,
		LivenessMetricPrefix:     gtmMetricPrefix + "property_liveness_errors",
		LivenessLookbackDuration: lookbackDuration,
		LivenessRegistry:         r,
		api:                      api,
		opts:                     opts,
		cache:                    newSampleCache(opts.CacheTTL),
		histograms:               make(map[string]prometheus.Histogram),
		now:                      time.Now,
		summaries:                make(map[string]prometheus.Summary),
	}
	for _, domain := range gtmMetricsConfig.Domains {
		for _, prop := range domain.Liveness {
			l.targets = append(l.targets, &livenessTarget{domain: domain.Name, cfg: prop, since: tstart, seen: map[livenessEvent]struct{}{}})
		}
	}
	return l
}

func (l *GTMLivenessTrafficExporter) observe(domain, property string, dcid int, duration float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := seriesKey(domain, property, strconv.Itoa(dcid))
	labels := prometheus.Labels{"domain": domain, "property": property, "datacenter": strconv.Itoa(dcid)}
	histo, ok := l.histograms[key]
	if !ok {
		histo = prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace:   l.LivenessMetricPrefix,
			Name:        "duration_per_datacenter_histogram",
			Help:        "Histogram of datacenter error duration (per domain and property)",
			ConstLabels: labels,
			Buckets:     durationBuckets,
		})
		l.LivenessRegistry.MustRegister(histo)
		l.histograms[key] = histo
	}
	summary, ok := l.summaries[key]
	if !ok {
		summary = prometheus.NewSummary(prometheus.SummaryOpts{
			Namespace:   l.LivenessMetricPrefix,
			Name:        "errors_per_datacenter_summary",
			Help:        "Summary of datacenter errors (per domain and property)",
			ConstLabels: labels,
			MaxAge:      l.LivenessLookbackDuration,
			BufCap:      prometheus.DefBufCap * 2,
		})
		l.LivenessRegistry.MustRegister(summary)
		l.summaries[key] = summary
	}
	histo.Observe(duration)
	summary.Observe(1)
}

func (l *GTMLivenessTrafficExporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- prometheus.NewDesc(l.LivenessMetricPrefix, "Akamai GTM Property Liveness Errors", nil, nil)
}

// Collect serves the samples fetched by the background poller.
func (l *GTMLivenessTrafficExporter) Collect(ch chan<- prometheus.Metric) {
	l.cache.collect(ch, l.now())
}

// Start polls the reporting API in the background until ctx is done.
func (l *GTMLivenessTrafficExporter) Start(ctx context.Context) {
	if len(l.targets) == 0 {
		return
	}
	go pollLoop(ctx, livenessCollectorName, l.opts.Interval, l.poll)
}

func (l *GTMLivenessTrafficExporter) poll(ctx context.Context) error {
	windowStart, windowEnd, err := fetchWindow(ctx, l.api.sessions[0], livenessCollectorName, "/gtm-api/v1/reports/liveness-tests/window")
	if err != nil {
		return err
	}
	var jobs []apiJob
	for _, t := range l.targets {
		// The report is per day: refetching it is only worth it once the window moved.
		if !windowEnd.After(t.fetchedThrough) {
			continue
		}
		t := t
		jobs = append(jobs, func(ctx context.Context, sess session.Session) {
			l.pollTarget(ctx, sess, t, windowStart, windowEnd)
		})
	}
	l.api.run(ctx, jobs)
	return nil
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func (l *GTMLivenessTrafficExporter) pollTarget(ctx context.Context, sess session.Session, t *livenessTarget, windowStart, windowEnd time.Time) {
	from := windowEnd.Add(-livenessLateness)
	if t.since.After(from) {
		from = t.since
	}
	if windowStart.After(from) {
		from = windowStart
	}
	firstDay := startOfDay(from)

	for day := firstDay; !day.After(windowEnd); day = day.Add(24 * time.Hour) {
		rows, err := l.fetchRows(ctx, sess, t, day)
		if err != nil {
			logrus.Warnf("Unable to get liveness report for property %s in domain %s ... Skipping. Error: %s", t.cfg.PropertyName, t.domain, err)
			return
		}
		now := l.now()
		for _, r := range rows {
			l.processRow(t, r.row, r.ts, now)
		}
	}
	t.fetchedThrough = windowEnd
	// Days no longer fetched cannot bring a duplicate back.
	for ev := range t.seen {
		if ev.ts.Before(firstDay) {
			delete(t.seen, ev)
		}
	}
}

func (l *GTMLivenessTrafficExporter) fetchRows(ctx context.Context, sess session.Session, t *livenessTarget, day time.Time) ([]pendingRow[*LivenessTData], error) {
	query := url.Values{}
	query.Set("date", day.Format(GTMTrafficDateFormat))
	// Without realTime the API returns an arbitrary slice of ~120 rows of the day.
	query.Set("realTime", "true")
	if t.cfg.TargetIP != "" {
		query.Set("targetIp", t.cfg.TargetIP) // takes priority over agentIp
	} else if t.cfg.AgentIP != "" {
		query.Set("agentIp", t.cfg.AgentIP)
	}
	path := fmt.Sprintf("/gtm-api/v1/reports/liveness-tests/domains/%s/properties/%s", t.domain, t.cfg.PropertyName)

	var report LivenessErrorsResponse
	if err := getJSON(ctx, sess, livenessCollectorName, "report", path, query, &report); err != nil {
		return nil, err
	}
	sortLivenessDataRowsByTimestamp(report.DataRows)

	var rows []pendingRow[*LivenessTData]
	for _, row := range report.DataRows {
		ts, err := parseTimeString(row.Timestamp, GTMTrafficLongTimeFormat)
		if err != nil {
			logrus.Errorf("Instance timestamp invalid ... Skipping. Error: %s", err)
			continue
		}
		if ts.After(t.since) {
			rows = append(rows, pendingRow[*LivenessTData]{row: row, ts: ts})
		}
	}
	return rows, nil
}

func (l *GTMLivenessTrafficExporter) processRow(t *livenessTarget, row *LivenessTData, ts, now time.Time) {
	prop := t.cfg
	// Rows already older than the exposure are still counted by the histograms.
	hold := ts.Add(l.opts.LivenessExposure).Sub(now)
	expose := func(key string, m prometheus.Metric) {
		if hold > 0 {
			l.cache.putUntil(key, m, now.Add(hold))
		}
	}
	failuresName := prometheus.BuildFQName(l.LivenessMetricPrefix, "", "datacenter_failures")
	durationName := prometheus.BuildFQName(l.LivenessMetricPrefix, "", "datacenter_failure_duration")
	tsLabel := ts.Format(time.RFC3339)

	for _, dc := range row.Datacenters {
		ev := livenessEvent{ts, dc.DatacenterID, dc.AgentIP, dc.TargetIP, dc.TrafficTargetName, dc.TestName, dc.ErrorCode}
		if _, dup := t.seen[ev]; dup {
			continue
		}
		t.seen[ev] = struct{}{}

		labelNames := []string{"domain", "property", "datacenter"}
		labelValues := []string{t.domain, prop.PropertyName, strconv.Itoa(dc.DatacenterID)}
		if prop.AgentIP == dc.AgentIP {
			labelNames = append(labelNames, "agentip")
			labelValues = append(labelValues, dc.AgentIP)
		}
		if prop.TargetIP == dc.TargetIP {
			labelNames = append(labelNames, "targetip")
			labelValues = append(labelValues, dc.TargetIP)
		}
		if prop.ErrorCode {
			labelNames = append(labelNames, "errorcode")
			labelValues = append(labelValues, fmt.Sprintf("%v", dc.ErrorCode))
		}
		if l.GTMConfig.TSLabel {
			labelNames = append(labelNames, "interval_timestamp")
			labelValues = append(labelValues, tsLabel)
		}

		failures := prometheus.MustNewConstMetric(
			prometheus.NewDesc(failuresName, "Number of datacenter failures (per domain, property, datacenter)", labelNames, nil),
			prometheus.CounterValue, 1, labelValues...)
		expose(seriesKey(failuresName, labelValues...), withReportTimestamp(l.GTMConfig, ts, failures))

		// A burst mixes rows with and without a duration; expose the longest
		// failure of the exposure window, not whichever row came last.
		if hold > 0 {
			durationDesc := prometheus.NewDesc(durationName, "Datacenter failure duration (per domain, property, datacenter)", labelNames, nil)
			values := labelValues
			l.cache.putMax(seriesKey(durationName, labelValues...), float64(dc.Duration), func(v float64) prometheus.Metric {
				return withReportTimestamp(l.GTMConfig, ts, prometheus.MustNewConstMetric(durationDesc, prometheus.GaugeValue, v, values...))
			}, now.Add(hold), now)
		}

		l.observe(t.domain, prop.PropertyName, dc.DatacenterID, float64(dc.Duration))
	}
}
