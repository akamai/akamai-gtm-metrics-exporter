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

package collectors

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"

	client "github.com/akamai/AkamaiOPEN-edgegrid-golang/client-v1"
	gtm "github.com/akamai/AkamaiOPEN-edgegrid-golang/reportsgtm-v1" // Note: imports ./configgtm-v1_3

	"strconv"
	"time"
)

var (
	// invalidMetricChars    = regexp.MustCompile("[^a-zA-Z0-9_:]")
	gtmDatacenterTrafficExporter GTMDatacenterTrafficExporter
)

type GTMDatacenterTrafficExporter struct {
	GTMConfig          GTMMetricsConfig
	DCMetricPrefix     string
	DCLookbackDuration time.Duration
	LastTimestamp      map[string]map[int]time.Time // index by domain, datacenterid
	DCRegistry         *prometheus.Registry
}

func NewDatacenterTrafficCollector(r *prometheus.Registry, gtmMetricsConfig GTMMetricsConfig, gtmMetricPrefix string, tstart time.Time, lookbackDuration time.Duration) *GTMDatacenterTrafficExporter {

	gtmDatacenterTrafficExporter = GTMDatacenterTrafficExporter{GTMConfig: gtmMetricsConfig, DCLookbackDuration: lookbackDuration}
	gtmDatacenterTrafficExporter.DCMetricPrefix = gtmMetricPrefix + "_datacenter_traffic_"
	gtmDatacenterTrafficExporter.DCLookbackDuration = lookbackDuration
	gtmDatacenterTrafficExporter.DCRegistry = r
	// Populate LastTimestamp per domain, datacenter. Start time applies to all.
	domainMap := make(map[string]map[int]time.Time)
	for _, domain := range gtmMetricsConfig.Domains {
		dcReqSummaryMap[domain.Name] = make(map[int]prometheus.Summary)
		dcReqsMap[domain.Name] = make(map[int][]int64)
		tStampMap := make(map[int]time.Time) // index by zone name
		for _, dc := range domain.Datacenters {
			tStampMap[dc.DatacenterID] = tstart

			// Create and register Summaries by domain, datacenter. TODO: property granualarity?
			dcSumMap := createDatacenterMaps(domain.Name, dc.DatacenterID)
			prometheus.MustRegister(dcSumMap)
		}
		domainMap[domain.Name] = tStampMap
	}
	gtmDatacenterTrafficExporter.LastTimestamp = domainMap

	return &gtmDatacenterTrafficExporter
}

// Metric Definitions
/*
var dnsHitsMetric = prometheus.NewDesc(prometheus.BuildFQName(namespace, "", "dns_hits_per_interval"), "Number of DNS hits per 5 minute interval (per zone)", []string{"zone"}, nil)
var nxdomainHitsMetric = prometheus.NewDesc(prometheus.BuildFQName(namespace, "", "nxdomain_hits_per_interval"), "Number of NXDomain hits per 5 minute interval (per zone)", []string{"zone"}, nil)
*/
// Summaries map by zone
var dcReqSummaryMap = make(map[string]map[int]prometheus.Summary)
var dcReqsMap = make(map[string]map[int][]int64)
var dcReqsMapCap int

// Initialize locally maintained maps. Only use domain and datacenter.
func createDatacenterMaps(domain string, dc int) prometheus.Summary {

	dclabel := strconv.Itoa(dc)
	labels := prometheus.Labels{"domain": domain, "datacenter": dclabel}

	dcReqSummaryMap[domain][dc] = prometheus.NewSummary(
		prometheus.SummaryOpts{
			Namespace:   gtmDatacenterTrafficExporter.DCMetricPrefix,
			Name:        "requests_per_interval_summary",
			Help:        "Number of aggregate datacenter requests per 5 minute interval (per domain)",
			MaxAge:      gtmDatacenterTrafficExporter.DCLookbackDuration,
			BufCap:      prometheus.DefBufCap * 2,
			ConstLabels: labels,
		})

	intervals := gtmDatacenterTrafficExporter.DCLookbackDuration / (time.Minute * 5)
	dcReqsMapCap = int(intervals)
	dcReqsMap[domain][dc] = make([]int64, 0, dcReqsMapCap)

	return dcReqSummaryMap[domain][dc]
}

// Describe function
func (d *GTMDatacenterTrafficExporter) Describe(ch chan<- *prometheus.Desc) {

	ch <- prometheus.NewDesc(d.DCMetricPrefix, "Akamai GTM Datacenter Traffic", nil, nil)
}

// Collect function
func (d *GTMDatacenterTrafficExporter) Collect(ch chan<- prometheus.Metric) {
	log.Debugf("Entering GTM DC Traffic Collect")

	endtime := time.Now().UTC() // Use same current time for all zones

	// Collect metrics for each domain and datacenter
	for _, domain := range d.GTMConfig.Domains {
		log.Debugf("Processing domain %s", domain.Name)
		for _, dc := range domain.Datacenters {
			// get last timestamp recorded. make sure diff > 5 mins.
			lasttime := d.LastTimestamp[domain.Name][dc.DatacenterID].Add(time.Minute)
			if endtime.Before(lasttime.Add(time.Minute * 5)) {
				lasttime = lasttime.Add(time.Minute * 5)
			}
			log.Debugf("Fetching datacenter Report for datacenter %d in domain %s.", dc.DatacenterID, domain.Name)
			dcTrafficReport, err := retrieveDatacenterTraffic(domain.Name, dc.DatacenterID, lasttime, endtime)
			if err != nil {
				apierr, ok := err.(client.APIError)
				if ok && apierr.Status == 500 {
					log.Warnf("Unable to get traffic report for datacenter %d. Internal error ... Skipping.", dc.DatacenterID)
					continue
				}
				log.Errorf("Unable to get traffic report for datacenter %d ... Skipping. Error: %s", dc.DatacenterID, err.Error())
				continue
			}
			log.Debugf("Traffic Metadata: [%v]", dcTrafficReport.Metadata)
			log.Debugf("Traffic data: [%v]", dcTrafficReport.DataRows)

			for _, reportInstance := range dcTrafficReport.DataRows {
				instanceTimestamp, err := parseTimeString(reportInstance.Timestamp, GTMTrafficLongTimeFormat)
				if err != nil {
					log.Errorf("Instance timestamp invalid  ... Skipping. Error: %s", err.Error())
					continue
				}
				if !instanceTimestamp.After(d.LastTimestamp[domain.Name][dc.DatacenterID]) {
					log.Debugf("Instance timestamp: [%v]. Last timestamp: [%v]", instanceTimestamp, d.LastTimestamp[domain.Name][dc.DatacenterID])
					log.Warnf("Attempting to re process report instance: [%v]. Skipping.", reportInstance)
					continue
				}
				// See if we missed an interval. Log warning for low
				log.Debugf("Instance timestamp: [%v]. Last timestamp: [%v]", instanceTimestamp, d.LastTimestamp[domain.Name][dc.DatacenterID])
				if instanceTimestamp.After(d.LastTimestamp[domain.Name][dc.DatacenterID].Add(time.Minute * (trafficReportInterval + 1))) {
					log.Warnf("Missing report interval. Current: %v, Last: %v", instanceTimestamp, d.LastTimestamp[domain.Name][dc.DatacenterID])
					/*
						reportInstance.Timestamp = e.LastTimestamp[zone].Add(time.Minute * trafficReportInterval)
						log.Debugf("Filling in entry with timestamp: %v", reportInstance.Timestamp)
						// Missed interval insert with averages
						dnsLen := int64(len(dnsHitsMap[zone]))
						var dnsHitsSum int64
						// calc current rolling dns sum
						for _, dhit := range dnsHitsMap[zone] {
							dnsHitsSum += dhit
						}
						if dnsLen > 0 {
							reportInstance.DNSHits = dnsHitsSum / dnsLen
						}
					*/
				}
				/*
							                // Update rolling hit sums
					                		dnsHitsLen := len(dnsHitsMap[zone])
					                        	if dnsHitsLen == hitsMapCap {
					                                	// Make room
					                                	dnsHitsMap[zone] = dnsHitsMap[zone][1:]
					                        	}
					                        	dnsHitsMap[zone] = append(dnsHitsMap[zone], reportInstance.DNSHits)
					                        	nxdHitsMap[zone] = append(nxdHitsMap[zone], reportInstance.NXDHits)
				*/

				var aggReqs int64
				var baseLabels = []string{"domain", "datacenter"}
				for _, instanceProp := range reportInstance.Properties {
					aggReqs += instanceProp.Requests // aggregate properties in scope
					if len(dc.Properties) > 0 {
						// create metric instance for properties in scope
						if stringSliceContains(dc.Properties, instanceProp.Name) {
							ts_labels := append(baseLabels, "property")
							if d.GTMConfig.TSLabel {
								ts_labels = append(ts_labels, "interval_timestamp")
							}
							ts := instanceTimestamp.Format(time.RFC3339)
							desc := prometheus.NewDesc(prometheus.BuildFQName(d.DCMetricPrefix, "", "datacenter_requests_per_interval"), "Number of datacenter requests per 5 minute interval (per domain)", ts_labels, nil)
							log.Debugf("Creating Requests metric. Domain: %s, Datacenter: %d, Property: %s, Requests: %v, Timestamp: %v", domain.Name, dc.DatacenterID, instanceProp.Name, float64(instanceProp.Requests), ts)
							var reqsmetric prometheus.Metric
							if d.GTMConfig.TSLabel {
								reqsmetric = prometheus.MustNewConstMetric(
									desc, prometheus.GaugeValue, float64(instanceProp.Requests), domain.Name, strconv.Itoa(dc.DatacenterID), instanceProp.Name, ts)
							} else {
								reqsmetric = prometheus.MustNewConstMetric(
									desc, prometheus.GaugeValue, float64(instanceProp.Requests), domain.Name, strconv.Itoa(dc.DatacenterID), instanceProp.Name)
							}
							if !d.GTMConfig.UseTimestamp {
								ch <- reqsmetric
							} else {
								ch <- prometheus.NewMetricWithTimestamp(instanceTimestamp, reqsmetric)
							}
						}
					}
				} // properties in time interval end
				if len(dc.Properties) < 1 {
					// Create agg instance
					ts_labels := baseLabels
					if d.GTMConfig.TSLabel {
						ts_labels = append(ts_labels, "interval_timestamp")
					}
					ts := instanceTimestamp.Format(time.RFC3339)
					desc := prometheus.NewDesc(prometheus.BuildFQName(d.DCMetricPrefix, "", "datacenter_requests_per_interval"), "Number of datacenter requests per 5 minute interval (per domain)", ts_labels, nil)
					log.Debugf("Creating Requests metric. Domain: %s, Datacenter: %d, Requests: %v, Timestamp: %v", domain.Name, dc.DatacenterID, float64(aggReqs), ts)
					var reqsmetric prometheus.Metric
					if d.GTMConfig.TSLabel {
						reqsmetric = prometheus.MustNewConstMetric(
							desc, prometheus.GaugeValue, float64(aggReqs), domain.Name, strconv.Itoa(dc.DatacenterID), ts)
					} else {
						reqsmetric = prometheus.MustNewConstMetric(
							desc, prometheus.GaugeValue, float64(aggReqs), domain.Name, strconv.Itoa(dc.DatacenterID))
					}
					if !d.GTMConfig.UseTimestamp {
						ch <- reqsmetric
					} else {
						ch <- prometheus.NewMetricWithTimestamp(instanceTimestamp, reqsmetric)
					}
				}
				// Update summary
				dcReqSummaryMap[domain.Name][dc.DatacenterID].Observe(float64(aggReqs))

				// Update last timestamp processed
				if instanceTimestamp.After(d.LastTimestamp[domain.Name][dc.DatacenterID]) {
					log.Debugf("Updating Last Timestamp from %v TO %v", d.LastTimestamp[domain.Name][dc.DatacenterID], instanceTimestamp)
					d.LastTimestamp[domain.Name][dc.DatacenterID] = instanceTimestamp
				}
				// only process one each interval!
				break
			} // interval end
		} // datacenter end
	} // domain end
}

func retrieveDatacenterTraffic(domain string, dc int, start, end time.Time) (*gtm.DcTrafficResponse, error) {

	qargs := make(map[string]string)
	// Get valid Traffic Window
	var err error
	dcTrafficWindow, err := gtm.GetDatacentersTrafficWindow()
	if err != nil {
		return nil, err
	}
	log.Debugf("Requested start: %v, end: %v", start, end)
	log.Debugf("Valid Traffic Window start: %v, end: %v", dcTrafficWindow.StartTime, dcTrafficWindow.EndTime)
	// Make sure provided start and end are in range
	if dcTrafficWindow.StartTime.Before(start) {
		qargs["start"], err = convertTimeFormat(start, time.RFC3339)
	} else {
		qargs["start"], err = convertTimeFormat(dcTrafficWindow.StartTime, time.RFC3339)
	}
	if err != nil {
		return nil, err
	}
	if dcTrafficWindow.EndTime.Before(end) {
		qargs["end"], err = convertTimeFormat(dcTrafficWindow.EndTime, time.RFC3339)
	} else {
		qargs["end"], err = convertTimeFormat(end, time.RFC3339)
	}
	if err != nil {
		return nil, err
	}
	log.Debugf("QARGS: %v", qargs)
	resp, err := gtm.GetTrafficPerDatacenter(domain, dc, qargs)
	if err != nil {
		return &gtm.DcTrafficResponse{}, err
	}
	//DataRows is list of pointers
	sortDCDataRowsByTimestamp(resp.DataRows)

	return resp, nil
}
