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
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v13/pkg/edgegrid"
	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v13/pkg/session"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

// PollOptions drives the background polling of the Akamai reporting API.
// Reports are fetched off the scrape path, Collect only serves the cache.
type PollOptions struct {
	Interval time.Duration // delay between two poll cycles
	CacheTTL time.Duration // how long the last traffic sample of a series stays exposed
	// SettleDelay only reads traffic buckets that ended at least this long ago.
	// Akamai keeps revising a bucket for ~80 min after it starts, an early read is low.
	SettleDelay time.Duration
	// LivenessExposure is how long after it happened a liveness error stays exposed.
	// Tied to the event time, so a restart never shows a past error as current.
	LivenessExposure time.Duration
}

const exporterNamespace = "akamai_gtm_metrics_exporter"

var (
	pollDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: exporterNamespace,
		Name:      "poll_duration_seconds",
		Help:      "Duration of the last Akamai reporting API poll cycle.",
	}, []string{"collector"})
	lastPollSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: exporterNamespace,
		Name:      "last_successful_poll_timestamp_seconds",
		Help:      "Unix time of the last poll cycle whose report window could be fetched.",
	}, []string{"collector"})
	apiRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: exporterNamespace,
		Name:      "api_requests_total",
		Help:      "Akamai reporting API requests, by API (window or report) and outcome.",
	}, []string{"collector", "api", "outcome"})
)

// RegisterPollMetrics registers the exporter self-monitoring metrics.
func RegisterPollMetrics(r prometheus.Registerer) {
	r.MustRegister(pollDuration, lastPollSuccess, apiRequests)
}

// APIPool holds one session per worker: session.Exec mutates its http.Client,
// so a session must never be shared between goroutines.
type APIPool struct {
	sessions []session.Session
}

// NewAPIPool creates size sessions signing with signer. A nil transport uses a
// clone of http.DefaultTransport.
func NewAPIPool(signer edgegrid.Signer, size int, timeout time.Duration, transport http.RoundTripper) (*APIPool, error) {
	if size < 1 {
		return nil, fmt.Errorf("api pool size must be at least 1, got %d", size)
	}
	if transport == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.MaxIdleConnsPerHost = size
		transport = t
	}
	pool := &APIPool{}
	for i := 0; i < size; i++ {
		sess, err := session.New(
			session.WithSigner(signer),
			session.WithClient(&http.Client{Timeout: timeout, Transport: transport}),
		)
		if err != nil {
			return nil, fmt.Errorf("session creation failed: %w", err)
		}
		pool.sessions = append(pool.sessions, sess)
	}
	return pool, nil
}

type apiJob func(ctx context.Context, sess session.Session)

// run executes jobs on one worker per session and waits for all of them.
func (p *APIPool) run(ctx context.Context, jobs []apiJob) {
	queue := make(chan apiJob)
	var wg sync.WaitGroup
	for _, sess := range p.sessions {
		wg.Add(1)
		go func(sess session.Session) {
			defer wg.Done()
			for job := range queue {
				job(ctx, sess)
			}
		}(sess)
	}
enqueue:
	for _, job := range jobs {
		select {
		case queue <- job:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(queue)
	wg.Wait()
}

// getJSON performs a signed GET and decodes a 200 response into out.
func getJSON(ctx context.Context, sess session.Session, collector, api, path string, query url.Values, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}
	resp, err := sess.Exec(req, out)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			err = fmt.Errorf("GET %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	apiRequests.WithLabelValues(collector, api, outcome).Inc()
	return err
}

// fetchWindow returns the time range for which reports are available.
func fetchWindow(ctx context.Context, sess session.Session, collector, path string) (time.Time, time.Time, error) {
	var w struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if err := getJSON(ctx, sess, collector, "window", path, nil, &w); err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to fetch report window: %w", err)
	}
	start, err := time.Parse(time.RFC3339, w.Start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid window start %q: %w", w.Start, err)
	}
	end, err := time.Parse(time.RFC3339, w.End)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid window end %q: %w", w.End, err)
	}
	return start, end, nil
}

const trafficBucket = trafficReportInterval * time.Minute

// trafficQueryRange reproduces the original boundary logic for the traffic
// reports. ok is false when no new report row can be available yet.
func trafficQueryRange(last, windowStart, windowEnd, now time.Time) (start, end time.Time, ok bool) {
	start = last.Add(time.Minute)
	if now.Before(start.Add(trafficBucket)) {
		start = start.Add(trafficBucket)
	}
	if !windowStart.Before(start) {
		start = windowStart
	} else if !windowEnd.After(start) {
		start = windowEnd
	} else if aligned := start.Truncate(trafficBucket); aligned.After(windowStart) {
		// The API answers 400 invalidDateRange to a range not covering a full 5 minute bucket.
		start = aligned
	}
	end = now.Truncate(trafficBucket)
	if windowEnd.Before(end) {
		end = windowEnd
	}
	return start, end, start.Before(end)
}

// settledWindowEnd caps the report window end to the buckets that ended at
// least delay ago.
func settledWindowEnd(windowEnd, now time.Time, delay time.Duration) time.Time {
	if delay <= 0 {
		return windowEnd
	}
	if cutoff := now.Add(-delay).Truncate(trafficBucket); cutoff.Before(windowEnd) {
		return cutoff
	}
	return windowEnd
}

// settled tells whether the bucket starting at ts ended by windowEnd.
func settled(ts, windowEnd time.Time, delay time.Duration) bool {
	return delay <= 0 || !ts.Add(trafficBucket).After(windowEnd)
}

// emptyReportCursor is where to resume after a report with no new row up to
// end: no row can exist before the bucket holding end.
func emptyReportCursor(last, end time.Time) time.Time {
	if cursor := end.Truncate(trafficBucket).Add(-time.Second); cursor.After(last) {
		return cursor
	}
	return last
}

// pendingRow is a fetched report row not exposed yet.
type pendingRow[R any] struct {
	row R
	ts  time.Time
}

// pollLoop runs poll right away, then every interval until ctx is done.
func pollLoop(ctx context.Context, collector string, interval time.Duration, poll func(context.Context) error) {
	run := func() {
		started := time.Now()
		if err := poll(ctx); err != nil {
			logrus.Warnf("%s poll failed: %v", collector, err)
		} else {
			lastPollSuccess.WithLabelValues(collector).SetToCurrentTime()
		}
		pollDuration.WithLabelValues(collector).Set(time.Since(started).Seconds())
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// sampleCache keeps the last sample of each series until it expires.
type sampleCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]cacheEntry
}

type cacheEntry struct {
	metric  prometheus.Metric
	value   float64
	expires time.Time
}

func newSampleCache(ttl time.Duration) *sampleCache {
	return &sampleCache{ttl: ttl, entries: make(map[string]cacheEntry)}
}

func (c *sampleCache) put(key string, m prometheus.Metric, now time.Time) {
	c.putUntil(key, m, now.Add(c.ttl))
}

func (c *sampleCache) putUntil(key string, m prometheus.Metric, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{metric: m, expires: expires}
}

// putMax keeps the highest value exposed for a series until the latest expiry.
func (c *sampleCache) putMax(key string, value float64, build func(float64) prometheus.Metric, expires, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && now.Before(e.expires) {
		if e.expires.After(expires) {
			expires = e.expires
		}
		if e.value >= value {
			c.entries[key] = cacheEntry{metric: e.metric, value: e.value, expires: expires}
			return
		}
	}
	c.entries[key] = cacheEntry{metric: build(value), value: value, expires: expires}
}

func (c *sampleCache) collect(ch chan<- prometheus.Metric, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, e := range c.entries {
		if !now.Before(e.expires) {
			delete(c.entries, key)
			continue
		}
		ch <- e.metric
	}
}

func seriesKey(name string, labelValues ...string) string {
	return name + "\xff" + strings.Join(labelValues, "\xff")
}

// withReportTimestamp attaches the report row timestamp unless disabled in config.
func withReportTimestamp(cfg GTMMetricsConfig, ts time.Time, m prometheus.Metric) prometheus.Metric {
	if cfg.UseTimestamp != nil && !*cfg.UseTimestamp {
		return m
	}
	return prometheus.NewMetricWithTimestamp(ts, m)
}
