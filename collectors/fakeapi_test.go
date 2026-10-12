package collectors

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v13/pkg/edgegrid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// fakeAPI is an in-memory GTM reporting API serving canned report rows.
type fakeAPI struct {
	srv     *httptest.Server
	latency time.Duration

	mu             sync.Mutex
	trafficWindow  [2]time.Time
	livenessWindow [2]time.Time
	dcRows         map[string][]*DatacenterTrafficData // by domain/datacenter id
	propRows       map[string][]*PropertyTrafficData   // by domain/property
	liveRows       map[string][]*LivenessTData         // by domain/property, all days
	calls          map[string]int                      // by endpoint kind
	inflight       int
	maxInflight    int
}

func newFakeAPI(t testing.TB) *fakeAPI {
	f := &fakeAPI{
		dcRows:   map[string][]*DatacenterTrafficData{},
		propRows: map[string][]*PropertyTrafficData{},
		liveRows: map[string][]*LivenessTData{},
		calls:    map[string]int{},
	}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.inflight++
	if f.inflight > f.maxInflight {
		f.maxInflight = f.inflight
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()
	time.Sleep(f.latency)

	if r.Header.Get("Authorization") == "" {
		http.Error(w, "unsigned request", http.StatusUnauthorized)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/gtm-api/v1/reports/")
	q := r.URL.Query()
	write := func(kind string, v interface{}) {
		f.calls[kind]++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	window := func(w [2]time.Time) map[string]string {
		return map[string]string{"start": w[0].Format(time.RFC3339), "end": w[1].Format(time.RFC3339)}
	}
	inRange := func(ts string) bool {
		t, _ := time.Parse(GTMTrafficLongTimeFormat, ts)
		start, _ := time.Parse(time.RFC3339, q.Get("start"))
		end, _ := time.Parse(time.RFC3339, q.Get("end"))
		return !t.Before(start) && !t.After(end)
	}
	parts := strings.Split(path, "/")
	switch {
	case path == "traffic/datacenters-window":
		write("dc-window", window(f.trafficWindow))
	case path == "traffic/properties-window":
		write("prop-window", window(f.trafficWindow))
	case path == "liveness-tests/window":
		write("live-window", window(f.livenessWindow))
	case len(parts) == 5 && parts[0] == "traffic" && parts[3] == "datacenters":
		rows := []*DatacenterTrafficData{}
		known, ok := f.dcRows[parts[2]+"/"+parts[4]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		for _, row := range known {
			if inRange(row.Timestamp) {
				rows = append(rows, row)
			}
		}
		write("dc-report", DcTrafficResponse{DataRows: rows})
	case len(parts) == 5 && parts[0] == "traffic" && parts[3] == "properties":
		rows := []*PropertyTrafficData{}
		known, ok := f.propRows[parts[2]+"/"+parts[4]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		for _, row := range known {
			if inRange(row.Timestamp) {
				rows = append(rows, row)
			}
		}
		write("prop-report", PropertyTrafficResponse{DataRows: rows})
	case len(parts) == 5 && parts[0] == "liveness-tests" && parts[3] == "properties":
		if q.Get("date") == "" {
			http.Error(w, "date is required", http.StatusBadRequest)
			return
		}
		// The live API returns a partial day without realTime=true.
		if q.Get("realTime") != "true" {
			http.Error(w, "realTime=true expected", http.StatusBadRequest)
			return
		}
		rows := []*LivenessTData{}
		known, ok := f.liveRows[parts[2]+"/"+parts[4]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		for _, row := range known {
			if strings.HasPrefix(row.Timestamp, q.Get("date")) {
				rows = append(rows, row)
			}
		}
		// The live API returns the newest row first.
		sort.Slice(rows, func(i, j int) bool { return rows[i].Timestamp > rows[j].Timestamp })
		write("live-report", LivenessErrorsResponse{DataRows: rows})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAPI) callCount(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[kind]
}

func (f *fakeAPI) pool(t testing.TB, size int) *APIPool {
	signer := &edgegrid.Config{
		Host:         f.srv.Listener.Addr().String(),
		ClientToken:  "akab-client-token",
		ClientSecret: "c2VjcmV0",
		AccessToken:  "akab-access-token",
		MaxBody:      edgegrid.MaxBodySize,
	}
	pool, err := NewAPIPool(signer, size, 5*time.Second, f.srv.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// sample is one exposed metric, flattened for assertions.
type sample struct {
	value     float64
	timestamp int64 // ms, 0 when the sample carries none
}

// collectSamples renders what a scrape of c exposes, keyed by `name{l1="v1",...}`.
func collectSamples(t testing.TB, c prometheus.Collector) map[string]sample {
	t.Helper()
	ch := make(chan prometheus.Metric, 1024)
	go func() {
		c.Collect(ch)
		close(ch)
	}()
	out := map[string]sample{}
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatal(err)
		}
		var labels []string
		for _, l := range pb.GetLabel() {
			labels = append(labels, fmt.Sprintf("%s=%q", l.GetName(), l.GetValue()))
		}
		key := fqName(m) + "{" + strings.Join(labels, ",") + "}"
		if _, dup := out[key]; dup {
			t.Fatalf("series %s exposed twice in one scrape", key)
		}
		s := sample{timestamp: pb.GetTimestampMs()}
		switch {
		case pb.Gauge != nil:
			s.value = pb.GetGauge().GetValue()
		case pb.Counter != nil:
			s.value = pb.GetCounter().GetValue()
		}
		out[key] = s
	}
	return out
}

func fqName(m prometheus.Metric) string {
	desc := m.Desc().String()
	start := strings.Index(desc, `fqName: "`) + len(`fqName: "`)
	return desc[start : start+strings.Index(desc[start:], `"`)]
}

func boolPtr(b bool) *bool { return &b }
