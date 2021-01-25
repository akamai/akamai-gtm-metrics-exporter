// Copyright 2020 Akamai Technologies, Inc.
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
	defaultlistenaddress = ":9802"
	namespace            = "gtm_metrics"
	//MinsInHour            = 60
	HoursInDay = 24
	//DaysInWeek            = 7
	trafficReportInterval = 5 // mins
	lookbackDefaultDays   = 1
	//intervalsPerDay       = (MinsInHour / trafficReportInterval) * HoursInDay
)

var (
	configFile           = kingpin.Flag("config.file", "Edge DNS Traffic exporter configuration file. Default: ./gtm_metrics_config.yml").Default("gtm_metrics_config.yml").String()
	listenAddress        = kingpin.Flag("web.listen-address", "The address to listen on for HTTP requests.").Default(defaultlistenaddress).String()
	edgegridHost         = kingpin.Flag("gtm.edgegrid-host", "The Akamai Edgegrid host auth credential.").String()
	edgegridClientSecret = kingpin.Flag("gtm.edgegrid-client-secret", "The Akamai Edgegrid client_secret credential.").String()
	edgegridClientToken  = kingpin.Flag("gtm.edgegrid-client-token", "The Akamai Edgegrid client_token credential.").String()
	edgegridAccessToken  = kingpin.Flag("gtm.edgegrid-access-token", "The Akamai Edgegrid access_token credential.").String()

	// invalidMetricChars    = regexp.MustCompile("[^a-zA-Z0-9_:]")
	lookbackDuration = time.Hour * HoursInDay * lookbackDefaultDays
	gtmMetricPrefix  = "akamai_gtm_"

	/*
			defaultLivenessTestConfig = LivenessTestConfig{}
			defaultTrafficDatacenterConfig = TrafficDatacenterConfig{
		      						Properties:     make([]string, 0),
							}
		      	defaultTrafficPropertyConfig = TrafficPropertyConfig{
		        					DatacenterIDs:	make([]int, 0),
		        					DCNicknames:	make([]string, 0),
		        					Targets:	make([]string, 0),
							}
			defaultDomainTraffic = DomainTraffic{
								Properties:	[]*TrafficPropertyConfig{&defaultTrafficPropertyConfig},
								Datacenters:	[]*TrafficDatacenterConfig{&defaultTrafficDatacenterConfig},
								Liveness:	[]*LivenessTestConfig{&defaultLivenessTestConfig},
							}

			defaultDomainTrafficConfig = DomainTrafficConfig{
								DomainConfigs: 	[]DomainTraffic{defaultDomainTraffic},
							}
			defaultConfig = Config{Domains: defaultDomainTrafficConfig}
	*/
)

/*
type DomainTraffic struct {
	Name		string				`yaml:"domain_name"`
	Properties	[]*TrafficPropertyConfig	`yaml:"properties,omitempty"`
	Datacenters	[]*TrafficDatacenterConfig	`yaml:"datacenters,omitempty"`
	Liveness	[]*LivenessTestConfig		`yaml:"liveness_tests,omitempty"`
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (c *DomainTraffic) UnmarshalYAML(unmarshal func(interface{}) error) error {
	*c = defaultDomainTraffic
	type plain DomainTraffic
	if err := unmarshal((*plain)(c)); err != nil {
		return err
	}
        log.Debugf("Domain: [%v]", *c)
	if c.Name == "" {
		return errors.New("required domain name is empty")
	}
	if (c.Properties == nil || len(c.Properties) < 1) && (c.Datacenters == nil || len(c.Datacenters) < 1) && (c.Liveness == nil || len(c.Liveness) < 1) {
		return errors.New("No property, datacenter or liveness configs to collect")
	}

	return nil
}

type TrafficPropertyConfig struct {
	Name		string		`yaml:"property_name"`
	DatacenterIDs	[]int		`yaml:"datacenter,omitempty"`
	DCNicknames	[]string	`yaml:"dc_nickname,omitempty"`
	Targets		[]string	`yaml:"target_name,omitempty"`
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (p *TrafficPropertyConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
        *p = defaultTrafficPropertyConfig
        type plain TrafficPropertyConfig
        if err := unmarshal((*plain)(p)); err != nil {
                return err
        }
        log.Debugf("Property: [%v]", *p)
        if p.Name == "" {
                return errors.New("required property name is empty")
        }

        return nil
}

type TrafficDatacenterConfig struct {
      DatacenterID	int		`yaml:"datacenter_id"`	  // Required
      Properties        []string        `yaml:"property,omitempty"`
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (d *TrafficDatacenterConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
        *d = defaultTrafficDatacenterConfig
        type plain TrafficDatacenterConfig
        if err := unmarshal((*plain)(d)); err != nil {
                return err
        }
        log.Debugf("Datacenter: [%v]", *d)
        if d.DatacenterID == 0 {
                return errors.New("required datacenter id is not set")
        }

        return nil
}

type LivenessTestConfig struct {
	PropertyName 	string		`yaml:"property_name"`
	AgentIp		string		`yaml:"agent_ip,omitempty"`
	TargetIp	string		`yaml:"target_ip,omitempty"`
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (p *LivenessTestConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
        *p = defaultLivenessTestConfig
        type plain LivenessTestConfig
        if err := unmarshal((*plain)(p)); err != nil {
                return err
        }
	log.Debugf("Liveness test: [%v]", *p)
        if p.PropertyName == "" {
                return errors.New("required property name is empty")
        }

        return nil
}

// Exporter config
type GTMMetricsConfig struct {
	Domains		[]*DomainTraffic	`yaml:"domains"`
	EdgercPath    	string  		`yaml:"edgerc_path"`
	EdgercSection 	string  		`yaml:"edgerc_section"`
	SummaryWindow 	string  		`yaml:"summary_window"` // mins, hours, days, [weeks]. Default lookbackDefaultDays
	TSLabel       bool     			`yaml:"timestamp_label"`   // Creates time series with traffic timestamp as label
	UseTimestamp  bool     			`yaml:"traffic_timestamp"` // Create time series with traffic timestamp
}

*/

/*
TODO: Remove?
type GTMMetricsExporter struct {
	MetricsExporterConfig 	GTMMetricsConfig
}

func NewGTMMetricsExporter(gtmMetricsConfig GTMMetricsConfig) *GTMMetricsExporter {

	// FIX
	return &GTMMetricsExporter{
		MetricsExporterConfig: gtmMetricsConfig,
	}
}
*/

// Initialize Akamai Edgegrid Config. Priority order:
// 1. Command line
// 2. Edgerc path
// 3. Environment
// 4. Default
func initAkamaiConfig(gtmMetricsConfig collectors.GTMMetricsConfig) error {

	if *edgegridHost != "" && *edgegridClientSecret != "" && *edgegridClientToken != "" && *edgegridAccessToken != "" {
		edgeconf := edgegrid.Config{}
		edgeconf.Host = *edgegridHost
		edgeconf.ClientToken = *edgegridClientSecret
		edgeconf.ClientSecret = *edgegridClientToken
		edgeconf.AccessToken = *edgegridAccessToken
		edgeconf.MaxBody = 131072
		return edgeInit(edgeconf)
	} else if *edgegridHost != "" || *edgegridClientSecret != "" || *edgegridClientToken != "" || *edgegridAccessToken != "" {
		log.Warnf("Command line Auth Keys are incomplete. Looking for alternate definitions.")
	}

	// Edgegrid will also check for environment variables ...
	err := EdgegridInit(gtmMetricsConfig.EdgercPath, gtmMetricsConfig.EdgercSection)
	if err != nil {
		log.Fatalf("Error initializing Akamai Edgegrid config: %s", err.Error())
		return err
	}

	log.Debugf("Edgegrid config: [%v]", edgegridConfig)

	return nil

}

// Calculate summary window duration based on config and save in lookbackDuration global variable
func calcSummaryWindowDuration(window string) error {

	var datawin int
	var err error
	var multiplier time.Duration = time.Hour * time.Duration(HoursInDay) // assume days

	log.Debugf("Window: %s", window)
	if window == "" {
		return fmt.Errorf("Summary window not set")
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
		return err
	}
	log.Debugf("multiplier: [%v} units: [%v]", multiplier, datawin)
	lookbackDuration = multiplier * time.Duration(datawin)
	return nil

}

/*
TODO: Remove?

// Describe function
func (e *GTMMetricsExporter) Describe(ch chan<- *prometheus.Desc) {

	ch <- prometheus.NewDesc(gtmMetricPrefix+"metrics_exporter", "Akamai Global Traffic Management Metrics", nil, nil)
}

func init() {
	prometheus.MustRegister(version.NewCollector(gtmMetricPrefix+"metrics_exporter"))
}
*/

func main() {

	log.AddFlags(kingpin.CommandLine)
	kingpin.Version(version.Print(gtmMetricPrefix + "metrics_exporter"))
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

	tstart := time.Now().UTC().Add(time.Minute * time.Duration(trafficReportInterval*-1)) // assume start time is Exporter launch less 5 mins
	if gtmMetricsConfig.SummaryWindow != "" {
		err = calcSummaryWindowDuration(gtmMetricsConfig.SummaryWindow)
		if err == nil {
			tstart = time.Now().UTC().Add(lookbackDuration * -1)
		} else {
			log.Warnf("Retention window is not valid. Using default (%d days)", lookbackDefaultDays)
		}
	} else {
		log.Warnf("Retention window is not configured. Using default (%d days)", lookbackDefaultDays)
	}

	log.Infof("GTM Metrics exporter start time: %v", tstart)

	/*
		// TODO:: Follow this pattern?
		errors := prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "digitalocean_errors_total",
			Help: "The total number of errors per collector",
		}, []string{"collector"})
	*/

	// TODO: Option to use custom Registry? ***Clean up***

	//r := prometheus.NewRegistry()
	prometheus.MustRegister(version.NewCollector(gtmMetricPrefix + "metrics_exporter"))
	//r.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	//r.MustRegister(prometheus.NewGoCollector())
	//r.MustRegister(errors)
	r := &prometheus.Registry{}
	prometheus.MustRegister(collectors.NewDatacenterTrafficCollector(r, gtmMetricsConfig, gtmMetricPrefix, tstart, lookbackDuration))
	prometheus.MustRegister(collectors.NewPropertyTrafficCollector(r, gtmMetricsConfig, gtmMetricPrefix, tstart, lookbackDuration))
	/*
		        r.MustRegister(NewGTMMetricsExporter(gtmMetricsConfig))
			r.MustRegister(collectors.NewLivenessTrafficCollector(gtmMetricsConfig, gtmMetricPrefix, tstart))
	*/
	http.Handle("/metrics", promhttp.Handler())
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
