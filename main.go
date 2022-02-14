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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"

	kingpin "gopkg.in/alecthomas/kingpin.v2"

	edgegrid "github.com/akamai/AkamaiOPEN-edgegrid-golang/edgegrid"
	collectors "github.com/akamai/akamai-gtm-metrics-exporter/collectors"
	"gopkg.in/yaml.v2"

	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
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

	// invalidMetricChars    = regexp.MustCompile("[^a-zA-Z0-9_:]")
	lookbackDuration = lookbackDefaultDuration
	prefillDuration  = prefillDefaultDuration
)

// Initialize Akamai Edgegrid Config. Priority order:
// 1. Command line
// 2. Edgerc path
// 3. Environment
// 4. Default
func initAkamaiConfig(gtmMetricsConfig collectors.GTMMetricsConfig) error {

	if *edgegridHost != "" && *edgegridClientSecret != "" && *edgegridClientToken != "" && *edgegridAccessToken != "" {
		edgeconf := edgegrid.Config{}
		edgeconf.Host = *edgegridHost
		edgeconf.ClientToken = *edgegridClientToken
		edgeconf.ClientSecret = *edgegridClientSecret
		edgeconf.AccessToken = *edgegridAccessToken
		edgeconf.MaxBody = 131072
		return collectors.EdgeInit(edgeconf)
	} else if *edgegridHost != "" || *edgegridClientSecret != "" || *edgegridClientToken != "" || *edgegridAccessToken != "" {
		log.Warnf("Command line Auth Keys are incomplete. Looking for alternate definitions.")
	}

	// Edgegrid will also check for environment variables ...
	err := collectors.EdgegridInit(gtmMetricsConfig.EdgercPath, gtmMetricsConfig.EdgercSection)
	if err != nil {
		log.Fatalf("Error initializing Akamai Edgegrid config: %s", err.Error())
		return err
	}

	log.Debugf("Edgegrid config: [%v]", collectors.EdgegridConfig)

	return nil

}

// Calculate window duration based on config and save in lookbackDuration global variable
func calcWindowDuration(window string) (time.Duration, error) {

	var datawin int
	var err error
	var multiplier time.Duration = time.Hour * time.Duration(HoursInDay) // assume days

	log.Debugf("Window: %s", window)
	if window == "" {
		return time.Second * 0, fmt.Errorf("Summary window not set")
	}
	iunit := window[len(window)-1:]
	if !strings.Contains("mhd", strings.ToLower(iunit)) {
		// no units. default days
		datawin, err = strconv.Atoi(window)
	} else {
		len := window[0 : len(window)-1]
		datawin, err = strconv.Atoi(len)
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
		log.Warnf("ERROR: %s", err.Error())
		return time.Second * 0, err
	}
	log.Debugf("multiplier: [%v} units: [%v]", multiplier, datawin)
	return multiplier * time.Duration(datawin), nil

}

func main() {

	log.AddFlags(kingpin.CommandLine)
	kingpin.Version(version.Print(namespace + "metrics_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	log.Infof("Config file: %s", *configFile)
	log.Infof("Starting GTM Metrics exporter. %s", version.Info())
	log.Infof("Build context: %s", version.BuildContext())

	gtmMetricsConfig, err := loadConfig(*configFile) // save?
	if err != nil {
		log.Fatalf("Error loading akamai_gtm_metrics_exporter config file: %v", err)
	}

	log.Debugf("Exporter configuration: [%v]", gtmMetricsConfig)

	// Initalize Akamai Edgegrid ...
	err = initAkamaiConfig(gtmMetricsConfig)
	if err != nil {
		log.Fatalf("Error initializing Akamai Edgegrid config: %s", err.Error())
	}

	tstart := time.Now().UTC().Add(-1 * prefillDuration) // assume start time is Exporter launch less default prefill
	if gtmMetricsConfig.SummaryWindow != "" {
		lookbackDuration, err = calcWindowDuration(gtmMetricsConfig.SummaryWindow)
		if err != nil {
			log.Warnf("Summary Retention window is not valid. Using default")
		}
	} else {
		log.Warnf("Summary Retention window is not configured. Using default")
	}
	if gtmMetricsConfig.PreFillWindow != "" {
		prefillDuration, err = calcWindowDuration(gtmMetricsConfig.PreFillWindow)
		if err == nil {
			tstart = time.Now().UTC().Add(prefillDuration * -1)
		} else {
			log.Warnf("Prefill window is not valid. Using default")
		}
	} else {
		log.Warnf("Prefill window is not configured. Using default")
	}

	log.Infof("GTM Metrics exporter start time: %v", tstart)

	// Use custom registry
	r := prometheus.NewRegistry()
	r.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	r.MustRegister(prometheus.NewGoCollector())
	r.MustRegister(version.NewCollector(namespace + "metrics_exporter"))
	r.MustRegister(collectors.NewDatacenterTrafficCollector(r, gtmMetricsConfig, namespace, tstart, lookbackDuration))
	r.MustRegister(collectors.NewPropertyTrafficCollector(r, gtmMetricsConfig, namespace, tstart, lookbackDuration))
	r.MustRegister(collectors.NewLivenessTrafficCollector(r, gtmMetricsConfig, namespace, tstart, lookbackDuration))

	http.Handle("/metrics", promhttp.HandlerFor(r, promhttp.HandlerOpts{Registry: r}))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>akamai_gtm_metrics_exporter</title></head>
			<body>
			<h1>akamai_gtm_metrics_exporter</h1>
			<p><a href="/metrics">Metrics</a></p>
			</body>
			</html>`))
	})

	log.Info("Beginning to serve on address ", *listenAddress)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))

}

func loadConfig(configFile string) (collectors.GTMMetricsConfig, error) {
	if fileExists(configFile) {
		// Load config from file
		configData, err := ioutil.ReadFile(configFile)
		if err != nil {
			return collectors.GTMMetricsConfig{}, err
		}
		log.Debugf("GTM metrics config file: %s", string(configData))
		return loadConfigContent(configData)
	}

	log.Infof("Config file %v does not exist, using default values", configFile)
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

	log.Info("akamai_gtm_metrics_exporter config loaded")
	return config, nil
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
