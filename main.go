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

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v13/pkg/edgegrid"
	"github.com/akamai/akamai-gtm-metrics-exporter/collectors"
	kingpin "github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
	promcollectors "github.com/prometheus/client_golang/prometheus/collectors"
	buildversion "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	"github.com/sirupsen/logrus"
	yaml "gopkg.in/yaml.v3"
)

const (
	defaultlistenaddress    = ":9800"
	namespace               = "akamai_gtm_"
	HoursInDay              = 24
	trafficReportInterval   = 5 // mins
	lookbackDefaultDuration = 2 * 24 * time.Hour
	prefillDefaultDuration  = 10 * time.Minute
)

var (
	configFile           = kingpin.Flag("config.file", "GTM Metrics exporter configuration file. Default: ./gtm_metrics_config.yml").Default("gtm_metrics_config.yml").String()
	listenAddress        = kingpin.Flag("web.listen-address", "The address to listen on for HTTP requests.").Default(defaultlistenaddress).String()
	edgegridHost         = kingpin.Flag("gtm.edgegrid-host", "The Akamai Edgegrid host auth credential.").String()
	edgegridClientSecret = kingpin.Flag("gtm.edgegrid-client-secret", "The Akamai Edgegrid client_secret credential.").String()
	edgegridClientToken  = kingpin.Flag("gtm.edgegrid-client-token", "The Akamai Edgegrid client_token credential.").String()
	edgegridAccessToken  = kingpin.Flag("gtm.edgegrid-access-token", "The Akamai Edgegrid access_token credential.").String()

	// Kept for logrus setup but hidden from original logic flow if not needed
	logLevel  = kingpin.Flag("log.level", "Set the logging level (debug, info, warn, error, fatal)").Default("info").String()
	logFormat = kingpin.Flag("log.format", "Set the log target and format.").Default("logger:stderr").String()

	pollInterval    = kingpin.Flag("poll.interval", "Interval between two polls of the Akamai reporting API.").Default("1m").Duration()
	pollConcurrency = kingpin.Flag("poll.concurrency", "Maximum concurrent Akamai API requests per collector.").Default("4").Int()
	apiTimeout      = kingpin.Flag("akamai.timeout", "Timeout of a single Akamai API request.").Default("30s").Duration()
	settleDelay     = kingpin.Flag("report.settle-delay", "Only read traffic report buckets that ended at least this long ago: Akamai keeps revising a bucket for over an hour after publishing it.").Default("0s").Duration()
	livenessExpose  = kingpin.Flag("liveness.exposure", "How long after it happened a liveness error stays exposed.").Default("15m").Duration()
	metricsTTL      = kingpin.Flag("metrics.ttl", "How long the last traffic report sample of a series stays exposed after it was fetched.").Default("10m").Duration()

	lookbackDuration = lookbackDefaultDuration
	prefillDuration  = prefillDefaultDuration
)

func initAkamaiConfig(gtmMetricsConfig collectors.GTMMetricsConfig) *edgegrid.Config {
	var config *edgegrid.Config
	var err error

	// Match original logic: Check if all flags are present
	if *edgegridHost != "" && *edgegridClientSecret != "" && *edgegridClientToken != "" && *edgegridAccessToken != "" {
		config = &edgegrid.Config{
			Host:         *edgegridHost,
			ClientToken:  *edgegridClientToken,
			ClientSecret: *edgegridClientSecret,
			AccessToken:  *edgegridAccessToken,
		}
	} else if *edgegridHost != "" || *edgegridClientSecret != "" || *edgegridClientToken != "" || *edgegridAccessToken != "" {
		logrus.Warn("Command line Auth Keys are incomplete. Looking for alternate definitions.")
	}

	if config == nil {
		options := []edgegrid.Option{edgegrid.WithEnv(true)}
		if gtmMetricsConfig.EdgercPath != "" {
			options = append(options, edgegrid.WithFile(gtmMetricsConfig.EdgercPath))
			options = append(options, edgegrid.WithSection(gtmMetricsConfig.EdgercSection))
		}

		config, err = edgegrid.New(options...)
		if err != nil {
			logrus.Fatalf("No valid Akamai authentication configuration found: %v", err)
		}
	}

	return config
}

func calcWindowDuration(window string) (time.Duration, error) {
	var datawin int
	var err error
	var multiplier = time.Hour * time.Duration(HoursInDay)

	logrus.Debugf("Window: %s", window)
	if window == "" {
		return time.Second * 0, fmt.Errorf("summary window not set")
	}
	iunit := window[len(window)-1:]
	if !strings.Contains("mhd", strings.ToLower(iunit)) {
		datawin, err = strconv.Atoi(window)
	} else {
		val := window[0 : len(window)-1]
		datawin, err = strconv.Atoi(val)
		if strings.ToLower(iunit) == "m" {
			multiplier = time.Minute
			if err == nil && datawin < trafficReportInterval {
				datawin = trafficReportInterval
			}
		} else if strings.ToLower(iunit) == "h" {
			multiplier = time.Hour
		}
	}
	if err != nil {
		logrus.Warnf("ERROR: %s", err.Error())
		return time.Second * 0, err
	}
	logrus.Debugf("multiplier: [%v} units: [%v]", multiplier, datawin)
	return multiplier * time.Duration(datawin), nil
}

func setupLogging(level string, format string) {
	lvl, err := logrus.ParseLevel(level)
	if err == nil {
		logrus.SetLevel(lvl)
	}
	if strings.Contains(format, "json=true") {
		logrus.SetFormatter(&logrus.JSONFormatter{})
	} else {
		logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	}
}

func main() {
	// 1. Kingpin Setup
	kingpin.Version(version.Print(namespace + "metrics_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	// 2. Logging Setup
	setupLogging(*logLevel, *logFormat)

	// 3. Print Startup Info
	logrus.Infof("Config file: %s", *configFile)
	logrus.Infof("Starting GTM Metrics exporter. %s", version.Info())
	logrus.Infof("Build context: %s", version.BuildContext())

	// 4. Load Config
	gtmMetricsConfig, err := loadConfig(*configFile)
	if err != nil {
		logrus.Fatalf("Error loading akamai_gtm_metrics_exporter config file: %v", err)
	}
	logrus.Debugf("Exporter configuration: [%v]", gtmMetricsConfig)

	// 5. Initialize Akamai credentials
	akamaiConfig := initAkamaiConfig(gtmMetricsConfig)
	newAPIPool := func() *collectors.APIPool {
		pool, err := collectors.NewAPIPool(akamaiConfig, *pollConcurrency, *apiTimeout, nil)
		if err != nil {
			logrus.Fatalf("Error initializing Akamai session: %v", err)
		}
		return pool
	}

	// 6. Time Window Calculations
	tstart := time.Now().UTC().Add(-1 * prefillDuration)

	if gtmMetricsConfig.SummaryWindow != "" {
		lookbackDuration, err = calcWindowDuration(gtmMetricsConfig.SummaryWindow)
		if err != nil {
			logrus.Warnf("Summary Retention window is not valid. Using default")
		}
	} else {
		logrus.Warnf("Summary Retention window is not configured. Using default")
	}

	if gtmMetricsConfig.PreFillWindow != "" {
		prefillDuration, err = calcWindowDuration(gtmMetricsConfig.PreFillWindow)
		if err == nil {
			tstart = time.Now().UTC().Add(prefillDuration * -1)
		} else {
			logrus.Warnf("Prefill window is not valid. Using default")
		}
	} else {
		logrus.Warnf("Prefill window is not configured. Using default")
	}

	logrus.Infof("GTM Metrics exporter start time: %v", tstart)

	// 7. Registry and Collectors
	ctx := context.Background()
	r := prometheus.NewRegistry()
	r.MustRegister(promcollectors.NewProcessCollector(promcollectors.ProcessCollectorOpts{}))
	r.MustRegister(promcollectors.NewGoCollector())
	r.MustRegister(buildversion.NewCollector(namespace + "metrics_exporter"))

	collectors.RegisterPollMetrics(r)

	// Reports are polled in the background, a scrape never waits on the Akamai API.
	pollOpts := collectors.PollOptions{Interval: *pollInterval, CacheTTL: *metricsTTL, SettleDelay: *settleDelay, LivenessExposure: *livenessExpose}
	dcCollector := collectors.NewDatacenterTrafficCollector(newAPIPool(), r, gtmMetricsConfig, namespace, tstart, lookbackDuration, pollOpts)
	propertyCollector := collectors.NewPropertyTrafficCollector(newAPIPool(), r, gtmMetricsConfig, namespace, tstart, lookbackDuration, pollOpts)
	livenessCollector := collectors.NewLivenessTrafficCollector(newAPIPool(), r, gtmMetricsConfig, namespace, tstart, lookbackDuration, pollOpts)
	r.MustRegister(dcCollector, propertyCollector, livenessCollector)
	dcCollector.Start(ctx)
	propertyCollector.Start(ctx)
	livenessCollector.Start(ctx)

	// 8. HTTP Handlers
	http.Handle("/metrics", promhttp.HandlerFor(r, promhttp.HandlerOpts{Registry: r}))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>
			<head><title>akamai_gtm_metrics_exporter</title></head>
			<body>
			<h1>akamai_gtm_metrics_exporter</h1>
			<p><a href="/metrics">Metrics</a></p>
			</body>
			</html>`))
	})

	logrus.Info("Beginning to serve on address ", *listenAddress)
	logrus.Fatal(http.ListenAndServe(*listenAddress, nil))
}

func loadConfig(configFile string) (collectors.GTMMetricsConfig, error) {
	if fileExists(configFile) {
		configData, err := os.ReadFile(configFile)
		if err != nil {
			return collectors.GTMMetricsConfig{}, err
		}
		logrus.Debugf("GTM metrics config file: %s", string(configData))
		return loadConfigContent(configData)
	}
	logrus.Infof("Config file %v does not exist, using default values", configFile)
	return collectors.GTMMetricsConfig{}, nil
}

func loadConfigContent(configData []byte) (collectors.GTMMetricsConfig, error) {
	domains := make([]*collectors.DomainTraffic, 0)
	domains = append(domains, &collectors.DefaultDomainTraffic)
	config := collectors.GTMMetricsConfig{Domains: domains}
	err := yaml.Unmarshal(configData, &config)
	if err != nil {
		return config, err
	}
	logrus.Info("akamai_gtm_metrics_exporter config loaded")
	return config, nil
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
