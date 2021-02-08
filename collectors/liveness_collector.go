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
	"fmt"
	client "github.com/akamai/AkamaiOPEN-edgegrid-golang/client-v1"
	gtm "github.com/akamai/AkamaiOPEN-edgegrid-golang/reportsgtm-v1" // Note: imports ./configgtm-v1_3
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	"strconv"
	"time"
)

var (
	gtmLivenessTrafficExporter GTMLivenessTrafficExporter
	durationBuckets            = []float64{60, 1800, 3600, 7200, 14400}
)

type GTMLivenessTrafficExporter struct {
	GTMConfig                GTMMetricsConfig
	LivenessMetricPrefix     string
	LivenessLookbackDuration time.Duration
	LastTimestamp            map[string]map[string]time.Time // index by domain, liveness
	LivenessRegistry         *prometheus.Registry
}

func NewLivenessTrafficCollector(r *prometheus.Registry, gtmMetricsConfig GTMMetricsConfig, gtmMetricPrefix string, tstart time.Time, lookbackDuration time.Duration) *GTMLivenessTrafficExporter {

	gtmLivenessTrafficExporter = GTMLivenessTrafficExporter{GTMConfig: gtmMetricsConfig, LivenessLookbackDuration: lookbackDuration}
	gtmLivenessTrafficExporter.LivenessMetricPrefix = gtmMetricPrefix + "property_liveness_errors"
	gtmLivenessTrafficExporter.LivenessLookbackDuration = lookbackDuration
	gtmLivenessTrafficExporter.LivenessRegistry = r
	// Populate LastTimestamp per domain, liveness. Start time applies to all.
	domainMap := make(map[string]map[string]time.Time)
	for _, domain := range gtmMetricsConfig.Domains {
		tStampMap := make(map[string]time.Time) // index by property name
		livenessDurationHistogramMap[domain.Name] = make(map[string]map[int]prometheus.Histogram)
		livenessErrorsSummaryMap[domain.Name] = make(map[string]map[int]prometheus.Summary)
		for _, prop := range domain.Liveness {
			livenessDurationHistogramMap[domain.Name][prop.PropertyName] = make(map[int]prometheus.Histogram)
			livenessErrorsSummaryMap[domain.Name][prop.PropertyName] = make(map[int]prometheus.Summary)
			tStampMap[prop.PropertyName] = tstart
		}
		domainMap[domain.Name] = tStampMap
	}
	gtmLivenessTrafficExporter.LastTimestamp = domainMap

	return &gtmLivenessTrafficExporter
}

// Summaries map by domain, property, datacenter
var livenessDurationHistogramMap = make(map[string]map[string]map[int]prometheus.Histogram)
var livenessErrorsSummaryMap = make(map[string]map[string]map[int]prometheus.Summary)

func (l *GTMLivenessTrafficExporter) getDatacenterHistogramMetrics(domain, property string, dcid int) map[string]interface{} {

	histMap := make(map[string]interface{})
	if histo, ok := livenessDurationHistogramMap[domain][property][dcid]; ok {
		histMap["duration"] = histo
	} else {
		// doesn't exist. need to create
		labels := prometheus.Labels{"domain": domain, "property": property, "datacenter": strconv.Itoa(dcid)}
		livenessDurationHistogramMap[domain][property][dcid] = prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace:   gtmLivenessTrafficExporter.LivenessMetricPrefix,
				Name:        "duration_per_datacenter_histogram",
				Help:        "Histogram of datacenter error duration (per domain and property)",
				ConstLabels: labels,
				Buckets:     durationBuckets,
			})
		l.LivenessRegistry.MustRegister(livenessDurationHistogramMap[domain][property][dcid])
		histMap["duration"] = livenessDurationHistogramMap[domain][property][dcid]
	}

	if esum, ok := livenessErrorsSummaryMap[domain][property][dcid]; ok {
		histMap["errors"] = esum
	} else {
		// doesn't exist. need to create
		labels := prometheus.Labels{"domain": domain, "property": property, "datacenter": strconv.Itoa(dcid)}
		livenessErrorsSummaryMap[domain][property][dcid] = prometheus.NewSummary(
			prometheus.SummaryOpts{
				Namespace:   gtmLivenessTrafficExporter.LivenessMetricPrefix,
				Name:        "errors_per_datacenter_summary",
				Help:        "Summary of datacenter errors  (per domain and property)",
				ConstLabels: labels,
				MaxAge:      gtmLivenessTrafficExporter.LivenessLookbackDuration,
				BufCap:      prometheus.DefBufCap * 2,
			})
		l.LivenessRegistry.MustRegister(livenessErrorsSummaryMap[domain][property][dcid])
		histMap["errors"] = livenessErrorsSummaryMap[domain][property][dcid]
	}

	return histMap

}

// Describe function
func (l *GTMLivenessTrafficExporter) Describe(ch chan<- *prometheus.Desc) {

	ch <- prometheus.NewDesc(l.LivenessMetricPrefix, "Akamai GTM Property Liveness Errors", nil, nil)
}

// Collect function
func (l *GTMLivenessTrafficExporter) Collect(ch chan<- prometheus.Metric) {
	log.Debugf("Entering GTM Property Liveness Errors Collect")

	endtime := time.Now().UTC() // Use same current time for all zones

	// Collect metrics for each domain and liveness
	for _, domain := range l.GTMConfig.Domains {
		log.Debugf("Processing domain %s", domain.Name)
		for _, prop := range domain.Liveness {
			// get last timestamp recorded. make sure diff > 5 mins.
			lasttime := l.LastTimestamp[domain.Name][prop.PropertyName].Add(time.Minute)
			log.Debugf("Fetching liveness errors Report for property %s in domain %s.", prop.PropertyName, domain.Name)
			livenessTrafficReport, err := retrieveLivenessTraffic(domain.Name, prop.PropertyName, prop.AgentIP, prop.TargetIP, lasttime)
			if err != nil {
				apierr, ok := err.(client.APIError)
				if ok && apierr.Status == 500 {
					log.Warnf("Unable to get liveness errors report for property %s. Internal error ... Skipping.", prop.PropertyName)
					continue
				}
				log.Errorf("Unable to get liveness report for property %s ... Skipping. Error: %s", prop.PropertyName, err.Error())
				continue
			}
			if len(livenessTrafficReport.DataRows) < 1 && endtime.Day() != lasttime.Day() {
				// We've probably crossed a day boundary. Bump last time
				lasttime = lasttime.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
				// get updated report
				livenessTrafficReport, err = retrieveLivenessTraffic(domain.Name, prop.PropertyName, prop.AgentIP, prop.TargetIP, lasttime)
				if err != nil {
					apierr, ok := err.(client.APIError)
					if ok && apierr.Status == 500 {
						log.Warnf("Unable to get liveness errors report for property %s. Internal error ... Skipping.", prop.PropertyName)
						continue
					}
					if ok && apierr.Status == 400 {
						log.Warnf("Unable to get liveness errors report for property %s. ... Skipping.", prop.PropertyName)
						log.Errorf("%s", err.Error())
						continue
					}

					log.Errorf("Unable to get liveness report for property %s ... Skipping. Error: %s", prop.PropertyName, err.Error())
					continue
				}
			}
			log.Debugf("Traffic Metadata: [%v]", livenessTrafficReport.Metadata)

			for _, reportInstance := range livenessTrafficReport.DataRows {
				instanceTimestamp, err := parseTimeString(reportInstance.Timestamp, GTMTrafficLongTimeFormat)
				if err != nil {
					log.Errorf("Instance timestamp invalid  ... Skipping. Error: %s", err.Error())
					continue
				}
				if !instanceTimestamp.After(l.LastTimestamp[domain.Name][prop.PropertyName]) {
					log.Debugf("Instance timestamp: [%v]. Last timestamp: [%v]", instanceTimestamp, l.LastTimestamp[domain.Name][prop.PropertyName])
					log.Warnf("Attempting to re process report instance: [%v]. Skipping.", reportInstance)
					continue
				}
				// See if we missed an interval. Log warning for low
				log.Debugf("Instance timestamp: [%v]. Last timestamp: [%v]", instanceTimestamp, l.LastTimestamp[domain.Name][prop.PropertyName])
				var baseLabels = []string{"domain", "property", "datacenter"}
				for _, instanceDC := range reportInstance.Datacenters {
					// create metrics for datacenters in property per timestamp
					var tsLabels = baseLabels
					labelVals := []string{domain.Name, prop.PropertyName, strconv.Itoa(instanceDC.DatacenterID)}

					if prop.AgentIP == instanceDC.AgentIP {
						tsLabels = append(tsLabels, "agentip")
						labelVals = append(labelVals, instanceDC.AgentIP)
					}
					if prop.TargetIP == instanceDC.TargetIP {
						tsLabels = append(tsLabels, "targetip")
						labelVals = append(labelVals, instanceDC.TargetIP)
					}
					if prop.ErrorCode {
						tsLabels = append(tsLabels, "errorcode")
						codestring := fmt.Sprintf("%v", instanceDC.ErrorCode)
						labelVals = append(labelVals, codestring)
					}
					ts := instanceTimestamp.Format(time.RFC3339)
					if l.GTMConfig.TSLabel {
						tsLabels = append(tsLabels, "interval_timestamp")
						labelVals = append(labelVals, ts)
					}
					desc := prometheus.NewDesc(prometheus.BuildFQName(l.LivenessMetricPrefix, "", "datacenter_failures"), "Number of datacenter failures (per domain, property, datacenter)", tsLabels, nil)
					log.Debugf("Creating error failures counter metric. Domain: %s, Property: %s, Datacenter: %d, Timestamp: %v", domain.Name, prop.PropertyName, instanceDC.DatacenterID, ts)
					var errorsmetric, durmetric prometheus.Metric
					errorsmetric = prometheus.MustNewConstMetric(
						desc, prometheus.CounterValue, 1, labelVals...)
					if l.GTMConfig.UseTimestamp != nil && !*l.GTMConfig.UseTimestamp {
						ch <- errorsmetric
					} else {
						ch <- prometheus.NewMetricWithTimestamp(instanceTimestamp, errorsmetric)
					}
					desc = prometheus.NewDesc(prometheus.BuildFQName(l.LivenessMetricPrefix, "", "datacenter_failure_duration"), "Datacenter falure duration (per domain, property, datacenter)", tsLabels, nil)
					log.Debugf("Creating failure duration gauge metric. Domain: %s, Property: %s, Datacenter: %d, Timestamp: %v", domain.Name, prop.PropertyName, instanceDC.DatacenterID, ts)
					durmetric = prometheus.MustNewConstMetric(
						desc, prometheus.GaugeValue, float64(instanceDC.Duration), labelVals...)
					if l.GTMConfig.UseTimestamp != nil && !*l.GTMConfig.UseTimestamp {
						ch <- durmetric
					} else {
						ch <- prometheus.NewMetricWithTimestamp(instanceTimestamp, durmetric)
					}
					maps := l.getDatacenterHistogramMetrics(domain.Name, prop.PropertyName, instanceDC.DatacenterID)
					maps["duration"].(prometheus.Histogram).Observe(float64(instanceDC.Duration))
					maps["errors"].(prometheus.Summary).Observe(float64(1))

				} // datacenter end

				// Update last timestamp processed
				if instanceTimestamp.After(l.LastTimestamp[domain.Name][prop.PropertyName]) {
					log.Debugf("Updating Last Timestamp from %v TO %v", l.LastTimestamp[domain.Name][prop.PropertyName], instanceTimestamp)
					l.LastTimestamp[domain.Name][prop.PropertyName] = instanceTimestamp
				}
				// only process one each interval!
				break
			} // interval end
		} // liveness end
	} // domain end
}

func retrieveLivenessTraffic(domain, prop, agentID, targetID string, start time.Time) (*LivenessErrorsResponse, error) {

	qargs := make(map[string]string)
	if len(targetID) > 0 {
		qargs["targetId"] = targetID // Takes priority
	}
	if len(agentID) > 0 {
		if len(targetID) > 0 {
			log.Warnf("Both agentId and targetId filters set. Using targetId ONLY")
		} else {
			qargs["agentId"] = agentID
		}
	}

	// Get valid Traffic Window
	var err error
	livenessTrafficWindow, err := gtm.GetLivenessTestsWindow()
	if err != nil {
		return nil, err
	}
	// Make sure provided start and end are in range
	if livenessTrafficWindow.StartTime.Before(start) {
		if livenessTrafficWindow.EndTime.After(start) {
			qargs["date"], err = convertTimeFormat(start, GTMTrafficDateFormat)
		} else {
			qargs["date"], err = convertTimeFormat(livenessTrafficWindow.EndTime, GTMTrafficDateFormat)
		}
	} else {
		qargs["date"], err = convertTimeFormat(livenessTrafficWindow.StartTime, GTMTrafficDateFormat)
	}
	if err != nil {
		return nil, err
	}
	resp, err := GetLivenessErrorsReport(domain, prop, qargs)
	if err != nil {
		return nil, err
	}
	/*
		// DEBUG
		meta := &LivenessTMeta{
			Date:     "2016-11-23",
			Domain:   "example.akadns.net",
			Property: "www",
			Uri:      "https://akab-xxxxxxxxxxxxxxxx-xxxxxxxxxxxxxxxx.luna.akamaiapis.net/gtm-api/v1/reports/liveness-tests/domains/example.akadns.net/properties/www?date=2016-11-23"}
		dcptr := &LivenessDRow{
			DatacenterID:      3201,
			AgentIP:           "204.1.136.239",
			TestName:          "Our defences",
			ErrorCode:         3101,
			Duration:          0,
			Nickname:          "Winterfell",
			TrafficTargetName: "Winterfell - 1.2.3.4",
			TargetIP:          "1.2.3.4"}
		ldrows := []*LivenessDRow{dcptr}
		ldrowptr := &LivenessTData{Timestamp: time.Now().UTC().Add(-10*time.Minute).Format(time.RFC3339), Datacenters: ldrows}
		resp = &LivenessErrorsResponse{
			Metadata: meta,
			DataRows: []*LivenessTData{ldrowptr},
		}
		// END DEBUG
	*/
	//DataRows is list of pointers
	sortLivenessDataRowsByTimestamp(resp.DataRows)

	return resp, nil
}
