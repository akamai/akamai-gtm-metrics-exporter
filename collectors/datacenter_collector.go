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

const datacenterCollectorName = "datacenters"

type GTMDatacenterTrafficExporter struct {
	GTMConfig          GTMMetricsConfig
	DCMetricPrefix     string
	DCLookbackDuration time.Duration
	DCRegistry         *prometheus.Registry
	api                *APIPool
	opts               PollOptions
	cache              *sampleCache
	targets            []*dcTarget
}

// dcTarget is only touched by the worker polling it.
type dcTarget struct {
	domain         string
	cfg            *TrafficDatacenterConfig
	summary        prometheus.Summary
	last           time.Time // timestamp of the last processed report row
	fetchedThrough time.Time // report window end at the last successful fetch
	pending        []pendingRow[*DatacenterTrafficData]
}

type DcTrafficResponse struct {
	Metadata    *Metadata                `json:"metadata"`
	DataRows    []*DatacenterTrafficData `json:"dataRows"`
	DataSummary interface{}              `json:"dataSummary"`
	Links       []interface{}            `json:"links"`
}

type DatacenterTrafficData struct {
	Timestamp  string             `json:"timestamp"`
	Properties []*TrafficProperty `json:"properties"`
}

func NewDatacenterTrafficCollector(api *APIPool, r *prometheus.Registry, gtmMetricsConfig GTMMetricsConfig, gtmMetricPrefix string, tstart time.Time, lookbackDuration time.Duration, opts PollOptions) *GTMDatacenterTrafficExporter {
	d := &GTMDatacenterTrafficExporter{
		GTMConfig:          gtmMetricsConfig,
		DCMetricPrefix:     gtmMetricPrefix + "datacenter_traffic",
		DCLookbackDuration: lookbackDuration,
		DCRegistry:         r,
		api:                api,
		opts:               opts,
		cache:              newSampleCache(opts.CacheTTL),
	}
	for _, domain := range gtmMetricsConfig.Domains {
		for _, dc := range domain.Datacenters {
			summary := prometheus.NewSummary(prometheus.SummaryOpts{
				Namespace:   d.DCMetricPrefix,
				Name:        "requests_per_interval_summary",
				Help:        "Number of aggregate datacenter requests per 5 minute interval (per domain)",
				MaxAge:      lookbackDuration,
				BufCap:      prometheus.DefBufCap * 2,
				ConstLabels: prometheus.Labels{"domain": domain.Name, "datacenter": strconv.Itoa(dc.DatacenterID)},
			})
			r.MustRegister(summary)
			d.targets = append(d.targets, &dcTarget{domain: domain.Name, cfg: dc, summary: summary, last: tstart})
		}
	}
	return d
}

// Describe function
func (d *GTMDatacenterTrafficExporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- prometheus.NewDesc(d.DCMetricPrefix, "Akamai GTM Datacenter Traffic", nil, nil)
}

// Collect serves the samples fetched by the background poller.
func (d *GTMDatacenterTrafficExporter) Collect(ch chan<- prometheus.Metric) {
	d.cache.collect(ch, time.Now())
}

// Start polls the reporting API in the background until ctx is done.
func (d *GTMDatacenterTrafficExporter) Start(ctx context.Context) {
	if len(d.targets) == 0 {
		return
	}
	go pollLoop(ctx, datacenterCollectorName, d.opts.Interval, d.poll)
}

func (d *GTMDatacenterTrafficExporter) poll(ctx context.Context) error {
	windowStart, windowEnd, err := fetchWindow(ctx, d.api.sessions[0], datacenterCollectorName, "/gtm-api/v1/reports/traffic/datacenters-window")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	windowEnd = settledWindowEnd(windowEnd, now, d.opts.SettleDelay)
	var jobs []apiJob
	for _, t := range d.targets {
		if len(t.pending) > 0 {
			d.processRow(t, t.pending[0].row, t.pending[0].ts)
			t.pending = t.pending[1:]
			continue
		}
		if !windowEnd.After(t.fetchedThrough) {
			continue
		}
		start, end, ok := trafficQueryRange(t.last, windowStart, windowEnd, now)
		if !ok {
			logrus.Debugf("No new report for datacenter %d in domain %s yet", t.cfg.DatacenterID, t.domain)
			continue
		}
		t := t
		jobs = append(jobs, func(ctx context.Context, sess session.Session) {
			d.pollTarget(ctx, sess, t, start, end, windowEnd)
		})
	}
	d.api.run(ctx, jobs)
	return nil
}

func (d *GTMDatacenterTrafficExporter) pollTarget(ctx context.Context, sess session.Session, t *dcTarget, start, end, windowEnd time.Time) {
	query := url.Values{}
	query.Set("start", start.UTC().Format(time.RFC3339))
	query.Set("end", end.UTC().Format(time.RFC3339))
	path := fmt.Sprintf("/gtm-api/v1/reports/traffic/domains/%s/datacenters/%d", t.domain, t.cfg.DatacenterID)

	var report DcTrafficResponse
	if err := getJSON(ctx, sess, datacenterCollectorName, "report", path, query, &report); err != nil {
		logrus.Warnf("Unable to get traffic report for datacenter %d in domain %s ... Skipping. Error: %s", t.cfg.DatacenterID, t.domain, err)
		return
	}
	sortDCDataRowsByTimestamp(report.DataRows)

	t.fetchedThrough = windowEnd
	var pending []pendingRow[*DatacenterTrafficData]
	for _, row := range report.DataRows {
		ts, err := parseTimeString(row.Timestamp, GTMTrafficLongTimeFormat)
		if err != nil {
			logrus.Errorf("Instance timestamp invalid ... Skipping. Error: %s", err)
			continue
		}
		if ts.After(t.last) && settled(ts, windowEnd, d.opts.SettleDelay) {
			pending = append(pending, pendingRow[*DatacenterTrafficData]{row: row, ts: ts})
		}
	}
	if len(pending) == 0 {
		// Without it a target with no traffic queries an ever growing range.
		t.last = emptyReportCursor(t.last, end)
		return
	}
	// One row per cycle, as the scrape-time collector did: one exposition
	// cannot hold two samples of the same series. The rest is queued.
	d.processRow(t, pending[0].row, pending[0].ts)
	t.pending = pending[1:]
}

func (d *GTMDatacenterTrafficExporter) processRow(t *dcTarget, row *DatacenterTrafficData, ts time.Time) {
	if ts.After(t.last.Add(time.Minute * (trafficReportInterval + 1))) {
		logrus.Warnf("Missing report interval. Current: %v, Last: %v", ts, t.last)
	}
	now := time.Now()
	name := prometheus.BuildFQName(d.DCMetricPrefix, "", "requests_per_interval")
	help := "Number of datacenter requests per 5 minute interval (per domain)"
	tsLabel := ts.Format(time.RFC3339)
	dcID := strconv.Itoa(t.cfg.DatacenterID)

	emit := func(labelNames, labelValues []string, value float64) {
		if d.GTMConfig.TSLabel {
			labelNames = append(labelNames, "interval_timestamp")
			labelValues = append(labelValues, tsLabel)
		}
		desc := prometheus.NewDesc(name, help, labelNames, nil)
		m := prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labelValues...)
		d.cache.put(seriesKey(name, labelValues...), withReportTimestamp(d.GTMConfig, ts, m), now)
	}

	var aggReqs int64
	for _, prop := range row.Properties {
		aggReqs += prop.Requests
		if len(t.cfg.Properties) > 0 && stringSliceContains(t.cfg.Properties, prop.Name) {
			emit([]string{"domain", "datacenter", "property"}, []string{t.domain, dcID, prop.Name}, float64(prop.Requests))
		}
	}
	if len(t.cfg.Properties) < 1 {
		emit([]string{"domain", "datacenter"}, []string{t.domain, dcID}, float64(aggReqs))
	}
	t.summary.Observe(float64(aggReqs))
	t.last = ts
}
