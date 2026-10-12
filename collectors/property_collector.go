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
	"time"

	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v13/pkg/session"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

// --- Property Traffic Structs ---

type PropertyTrafficResponse struct {
	Metadata    PropertyTMeta          `json:"metadata"`
	DataRows    []*PropertyTrafficData `json:"dataRows"`
	DataSummary interface{}            `json:"dataSummary"` // Added for parity
	Links       []interface{}          `json:"links"`
}

type PropertyTrafficData struct {
	Timestamp   string            `json:"timestamp"`
	Datacenters []*PropertyDCData `json:"datacenters"` // Changed to pointer slice
}

type PropertyDCData struct {
	Nickname          string `json:"nickname"`
	DatacenterId      int    `json:"datacenterId"`
	TrafficTargetName string `json:"trafficTargetName"`
	Requests          int64  `json:"requests"`
	Status            string `json:"status"` // Added missing field
}

// --- Liveness/Errors Structs ---

type LivenessErrorsResponse struct {
	Metadata    *LivenessTMeta   `json:"metadata"`
	DataRows    []*LivenessTData `json:"dataRows"`
	DataSummary interface{}      `json:"dataSummary"`
	Links       []interface{}    `json:"links"`
}

type LivenessTData struct {
	Timestamp   string          `json:"timestamp"`
	Datacenters []*LivenessDRow `json:"datacenters"`
}

// Ensure your LivenessDRow and Metadata structs look like this:
type PropertyTMeta struct {
	URI      string `json:"uri"`
	Domain   string `json:"domain"`
	Interval string `json:"interval,omitempty"`
	Property string `json:"property"`
	Start    string `json:"start"`
	End      string `json:"end"`
}

const propertyCollectorName = "properties"

type GTMPropertyTrafficExporter struct {
	GTMConfig                GTMMetricsConfig
	PropertyMetricPrefix     string
	PropertyLookbackDuration time.Duration
	PropertyRegistry         *prometheus.Registry
	api                      *APIPool
	opts                     PollOptions
	cache                    *sampleCache
	targets                  []*propertyTarget
}

// propertyTarget is only touched by the worker polling it.
type propertyTarget struct {
	domain         string
	cfg            *TrafficPropertyConfig
	summary        prometheus.Summary
	last           time.Time // timestamp of the last processed report row
	fetchedThrough time.Time // report window end at the last successful fetch
	pending        []pendingRow[*PropertyTrafficData]
}

func NewPropertyTrafficCollector(api *APIPool, r *prometheus.Registry, gtmMetricsConfig GTMMetricsConfig, gtmMetricPrefix string, tstart time.Time, lookbackDuration time.Duration, opts PollOptions) *GTMPropertyTrafficExporter {
	p := &GTMPropertyTrafficExporter{
		GTMConfig:                gtmMetricsConfig,
		PropertyMetricPrefix:     gtmMetricPrefix + "property_traffic",
		PropertyLookbackDuration: lookbackDuration,
		PropertyRegistry:         r,
		api:                      api,
		opts:                     opts,
		cache:                    newSampleCache(opts.CacheTTL),
	}
	for _, domain := range gtmMetricsConfig.Domains {
		for _, prop := range domain.Properties {
			summary := prometheus.NewSummary(prometheus.SummaryOpts{
				Namespace:   p.PropertyMetricPrefix,
				Name:        "requests_per_interval_summary",
				Help:        "Number of aggregate property requests per 5 minute interval (per domain)",
				MaxAge:      lookbackDuration,
				BufCap:      prometheus.DefBufCap * 2,
				ConstLabels: prometheus.Labels{"domain": domain.Name, "property": prop.Name},
			})
			r.MustRegister(summary)
			p.targets = append(p.targets, &propertyTarget{domain: domain.Name, cfg: prop, summary: summary, last: tstart})
		}
	}
	return p
}

func (p *GTMPropertyTrafficExporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- prometheus.NewDesc(p.PropertyMetricPrefix, "Akamai GTM Property Traffic", nil, nil)
}

// Collect serves the samples fetched by the background poller.
func (p *GTMPropertyTrafficExporter) Collect(ch chan<- prometheus.Metric) {
	p.cache.collect(ch, time.Now())
}

// Start polls the reporting API in the background until ctx is done.
func (p *GTMPropertyTrafficExporter) Start(ctx context.Context) {
	if len(p.targets) == 0 {
		return
	}
	go pollLoop(ctx, propertyCollectorName, p.opts.Interval, p.poll)
}

func (p *GTMPropertyTrafficExporter) poll(ctx context.Context) error {
	windowStart, windowEnd, err := fetchWindow(ctx, p.api.sessions[0], propertyCollectorName, "/gtm-api/v1/reports/traffic/properties-window")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	windowEnd = settledWindowEnd(windowEnd, now, p.opts.SettleDelay)
	var jobs []apiJob
	for _, t := range p.targets {
		if len(t.pending) > 0 {
			p.processRow(t, t.pending[0].row, t.pending[0].ts)
			t.pending = t.pending[1:]
			continue
		}
		if !windowEnd.After(t.fetchedThrough) {
			continue
		}
		start, end, ok := trafficQueryRange(t.last, windowStart, windowEnd, now)
		if !ok {
			logrus.Debugf("No new report for property %s in domain %s yet", t.cfg.Name, t.domain)
			continue
		}
		t := t
		jobs = append(jobs, func(ctx context.Context, sess session.Session) {
			p.pollTarget(ctx, sess, t, start, end, windowEnd)
		})
	}
	p.api.run(ctx, jobs)
	return nil
}

func (p *GTMPropertyTrafficExporter) pollTarget(ctx context.Context, sess session.Session, t *propertyTarget, start, end, windowEnd time.Time) {
	query := url.Values{}
	query.Set("start", start.UTC().Format(time.RFC3339))
	query.Set("end", end.UTC().Format(time.RFC3339))
	path := fmt.Sprintf("/gtm-api/v1/reports/traffic/domains/%s/properties/%s", t.domain, t.cfg.Name)

	var report PropertyTrafficResponse
	if err := getJSON(ctx, sess, propertyCollectorName, "report", path, query, &report); err != nil {
		logrus.Warnf("Unable to get traffic report for property %s in domain %s ... Skipping. Error: %s", t.cfg.Name, t.domain, err)
		return
	}
	sortPropertyDataRowsByTimestamp(report.DataRows)

	t.fetchedThrough = windowEnd
	var pending []pendingRow[*PropertyTrafficData]
	for _, row := range report.DataRows {
		ts, err := parseTimeString(row.Timestamp, GTMTrafficLongTimeFormat)
		if err != nil {
			logrus.Errorf("Instance timestamp invalid ... Skipping. Error: %s", err)
			continue
		}
		if ts.After(t.last) && settled(ts, windowEnd, p.opts.SettleDelay) {
			pending = append(pending, pendingRow[*PropertyTrafficData]{row: row, ts: ts})
		}
	}
	if len(pending) == 0 {
		// Without it a target with no traffic queries an ever growing range.
		t.last = emptyReportCursor(t.last, end)
		return
	}
	// One row per cycle, as the scrape-time collector did: one exposition
	// cannot hold two samples of the same series. The rest is queued.
	p.processRow(t, pending[0].row, pending[0].ts)
	t.pending = pending[1:]
}

func (p *GTMPropertyTrafficExporter) processRow(t *propertyTarget, row *PropertyTrafficData, ts time.Time) {
	if ts.After(t.last.Add(time.Minute * (trafficReportInterval + 1))) {
		logrus.Warnf("Missing report interval. Current: %v, Last: %v", ts, t.last)
	}
	now := time.Now()
	name := prometheus.BuildFQName(p.PropertyMetricPrefix, "", "requests_per_interval")
	help := "Number of property requests per 5 minute interval (per domain)"
	tsLabel := ts.Format(time.RFC3339)
	prop := t.cfg

	emit := func(labelNames, labelValues []string, value float64) {
		if p.GTMConfig.TSLabel {
			labelNames = append(labelNames, "interval_timestamp")
			labelValues = append(labelValues, tsLabel)
		}
		desc := prometheus.NewDesc(name, help, labelNames, nil)
		m := prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labelValues...)
		p.cache.put(seriesKey(name, labelValues...), withReportTimestamp(p.GTMConfig, ts, m), now)
	}

	filtered := len(prop.DatacenterIDs) > 0 || len(prop.DCNicknames) > 0 || len(prop.Targets) > 0
	var aggReqs int64
	for _, dc := range row.Datacenters {
		aggReqs += dc.Requests
		if !filtered {
			continue
		}
		var filterLabel, filterVal string
		if intSliceContains(prop.DatacenterIDs, dc.DatacenterId) {
			filterLabel, filterVal = "datacenterid", strconv.Itoa(dc.DatacenterId)
		} else if stringSliceContains(prop.DCNicknames, dc.Nickname) {
			filterLabel, filterVal = "nickname", dc.Nickname
		} else if stringSliceContains(prop.Targets, dc.TrafficTargetName) {
			filterLabel, filterVal = "target", dc.TrafficTargetName
		}
		if filterVal != "" {
			emit([]string{"domain", "property", filterLabel}, []string{t.domain, prop.Name, filterVal}, float64(dc.Requests))
		}
	}
	if !filtered {
		emit([]string{"domain", "property"}, []string{t.domain, prop.Name}, float64(aggReqs))
	}
	t.summary.Observe(float64(aggReqs))
	t.last = ts
}
