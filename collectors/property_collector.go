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
	client "github.com/akamai/AkamaiOPEN-edgegrid-golang/client-v1"
	gtm "github.com/akamai/AkamaiOPEN-edgegrid-golang/reportsgtm-v1" // Note: imports ./configgtm-v1_3
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"

	"strconv"
	"time"
)

var (
	gtmPropertyTrafficExporter GTMPropertyTrafficExporter
)

type GTMPropertyTrafficExporter struct {
	GTMConfig                GTMMetricsConfig
	PropertyMetricPrefix     string
	PropertyLookbackDuration time.Duration
	LastTimestamp            map[string]map[string]time.Time // index by domain, property
	PropertyRegistry         *prometheus.Registry
}

func NewPropertyTrafficCollector(r *prometheus.Registry, gtmMetricsConfig GTMMetricsConfig, gtmMetricPrefix string, tstart time.Time, lookbackDuration time.Duration) *GTMPropertyTrafficExporter {

	gtmPropertyTrafficExporter = GTMPropertyTrafficExporter{GTMConfig: gtmMetricsConfig, PropertyLookbackDuration: lookbackDuration}
	gtmPropertyTrafficExporter.PropertyMetricPrefix = gtmMetricPrefix + "property_traffic"
	gtmPropertyTrafficExporter.PropertyLookbackDuration = lookbackDuration
	gtmPropertyTrafficExporter.PropertyRegistry = r
	// Populate LastTimestamp per domain, property. Start time applies to all.
	domainMap := make(map[string]map[string]time.Time)
	for _, domain := range gtmMetricsConfig.Domains {
		propertyReqSummaryMap[domain.Name] = make(map[string]prometheus.Summary)
		tStampMap := make(map[string]time.Time) // index by zone name
		for _, prop := range domain.Properties {
			tStampMap[prop.Name] = tstart

			// Create and register Summaries by domain, property. TODO: finer granualarity?
			propertySumMap := createPropertyMaps(domain.Name, prop.Name)
			r.MustRegister(propertySumMap)
		}
		domainMap[domain.Name] = tStampMap
	}
	gtmPropertyTrafficExporter.LastTimestamp = domainMap

	return &gtmPropertyTrafficExporter
}

// Summaries map by domain and property
var propertyReqSummaryMap = make(map[string]map[string]prometheus.Summary)

// Initialize locally maintained maps. Only use domain and property.
func createPropertyMaps(domain, prop string) prometheus.Summary {

	labels := prometheus.Labels{"domain": domain, "property": prop}

	propertyReqSummaryMap[domain][prop] = prometheus.NewSummary(
		prometheus.SummaryOpts{
			Namespace:   gtmPropertyTrafficExporter.PropertyMetricPrefix,
			Name:        "requests_per_interval_summary",
			Help:        "Number of aggregate property requests per 5 minute interval (per domain)",
			MaxAge:      gtmPropertyTrafficExporter.PropertyLookbackDuration,
			BufCap:      prometheus.DefBufCap * 2,
			ConstLabels: labels,
		})

	return propertyReqSummaryMap[domain][prop]
}

// Describe function
func (p *GTMPropertyTrafficExporter) Describe(ch chan<- *prometheus.Desc) {

	ch <- prometheus.NewDesc(p.PropertyMetricPrefix, "Akamai GTM Property Traffic", nil, nil)
}

// Collect function
func (p *GTMPropertyTrafficExporter) Collect(ch chan<- prometheus.Metric) {
	log.Debugf("Entering GTM Property Traffic Collect")

	endtime := time.Now().UTC() // Use same current time for all zones

	// Collect metrics for each domain and property
	for _, domain := range p.GTMConfig.Domains {
		log.Debugf("Processing domain %s", domain.Name)
		for _, prop := range domain.Properties {
			// get last timestamp recorded. make sure diff > 5 mins.
			lasttime := p.LastTimestamp[domain.Name][prop.Name].Add(time.Minute)
			if endtime.Before(lasttime.Add(time.Minute * 5)) {
				lasttime = lasttime.Add(time.Minute * 5)
			}
			log.Debugf("Fetching property Report for property %s in domain %s.", prop.Name, domain.Name)
			propertyTrafficReport, err := retrievePropertyTraffic(domain.Name, prop.Name, lasttime, endtime)
			if err != nil {
				apierr, ok := err.(client.APIError)
				if ok && apierr.Status == 500 {
					log.Warnf("Unable to get traffic report for property %s. Internal error ... Skipping.", prop.Name)
					continue
				}
				if ok && apierr.Status == 400 {
					log.Warnf("Unable to get traffic report for property %s. Internal ... Skipping.", prop.Name)
					log.Errorf("%s", err.Error())
					continue
				}
				log.Errorf("Unable to get traffic report for property %s ... Skipping. Error: %s", prop.Name, err.Error())
				continue
			}
			log.Debugf("Traffic Metadata: [%v]", propertyTrafficReport.Metadata)
			for _, reportInstance := range propertyTrafficReport.DataRows {
				instanceTimestamp, err := parseTimeString(reportInstance.Timestamp, GTMTrafficLongTimeFormat)
				if err != nil {
					log.Errorf("Instance timestamp invalid  ... Skipping. Error: %s", err.Error())
					continue
				}
				if !instanceTimestamp.After(p.LastTimestamp[domain.Name][prop.Name]) {
					log.Debugf("Instance timestamp: [%v]. Last timestamp: [%v]", instanceTimestamp, p.LastTimestamp[domain.Name][prop.Name])
					log.Warnf("Attempting to re process report instance: [%v]. Skipping.", reportInstance)
					continue
				}
				// See if we missed an interval. Log warning for low
				log.Debugf("Instance timestamp: [%v]. Last timestamp: [%v]", instanceTimestamp, p.LastTimestamp[domain.Name][prop.Name])
				if instanceTimestamp.After(p.LastTimestamp[domain.Name][prop.Name].Add(time.Minute * (trafficReportInterval + 1))) {
					log.Warnf("Missing report interval. Current: %v, Last: %v", instanceTimestamp, p.LastTimestamp[domain.Name][prop.Name])
				}

				var aggReqs int64
				var baseLabels = []string{"domain", "property"}
				for _, instanceDC := range reportInstance.Datacenters {
					aggReqs += instanceDC.Requests // aggregate properties in scope
					if len(prop.DatacenterIDs) > 0 || len(prop.DCNicknames) > 0 || len(prop.Targets) > 0 {
						// create metric instance for properties in scope
						var tsLabels []string
						var filterVal string
						var filterLabel string
						if intSliceContains(prop.DatacenterIDs, instanceDC.DatacenterId) {
							filterVal = strconv.Itoa(instanceDC.DatacenterId)
							filterLabel = "datacenterid"
							tsLabels = append(baseLabels, filterLabel)
						} else if stringSliceContains(prop.DCNicknames, instanceDC.Nickname) {
							filterVal = instanceDC.Nickname
							filterLabel = "nickname"
							tsLabels = append(baseLabels, filterLabel)
						} else if stringSliceContains(prop.Targets, instanceDC.TrafficTargetName) {
							filterVal = instanceDC.TrafficTargetName
							filterLabel = "target"
							tsLabels = append(baseLabels, filterLabel)
						}
						if filterVal != "" {
							// Match!
							if p.GTMConfig.TSLabel {
								tsLabels = append(tsLabels, "interval_timestamp")
							}
							ts := instanceTimestamp.Format(time.RFC3339)
							desc := prometheus.NewDesc(prometheus.BuildFQName(p.PropertyMetricPrefix, "", "requests_per_interval"), "Number of property requests per 5 minute interval (per domain)", tsLabels, nil)
							log.Debugf("Creating Requests metric. Domain: %s, Property: %s, %s: %s, Requests: %v, Timestamp: %v", domain.Name, prop.Name, filterLabel, filterVal, float64(instanceDC.Requests), ts)
							var reqsmetric prometheus.Metric
							if p.GTMConfig.TSLabel {
								reqsmetric = prometheus.MustNewConstMetric(
									desc, prometheus.GaugeValue, float64(instanceDC.Requests), domain.Name, prop.Name, filterVal, ts)
							} else {
								reqsmetric = prometheus.MustNewConstMetric(
									desc, prometheus.GaugeValue, float64(instanceDC.Requests), domain.Name, prop.Name, filterVal)
							}
							if p.GTMConfig.UseTimestamp != nil && !*p.GTMConfig.UseTimestamp {
								ch <- reqsmetric
							} else {
								ch <- prometheus.NewMetricWithTimestamp(instanceTimestamp, reqsmetric)
							}
						}
					}
				} // properties in time interval end

				if len(prop.DatacenterIDs) < 1 && len(prop.DCNicknames) < 1 && len(prop.Targets) < 1 {
					// No filters. Create agg instance
					tsLabels := baseLabels
					if p.GTMConfig.TSLabel {
						tsLabels = append(tsLabels, "interval_timestamp")
					}
					ts := instanceTimestamp.Format(time.RFC3339)
					desc := prometheus.NewDesc(prometheus.BuildFQName(p.PropertyMetricPrefix, "", "requests_per_interval"), "Number of property requests per 5 minute interval (per domain)", tsLabels, nil)
					log.Debugf("Creating Requests metric. Domain: %s, Property: %s, Requests: %v, Timestamp: %v", domain.Name, prop.Name, float64(aggReqs), ts)
					var reqsmetric prometheus.Metric
					if p.GTMConfig.TSLabel {
						reqsmetric = prometheus.MustNewConstMetric(
							desc, prometheus.GaugeValue, float64(aggReqs), domain.Name, prop.Name, ts)
					} else {
						reqsmetric = prometheus.MustNewConstMetric(
							desc, prometheus.GaugeValue, float64(aggReqs), domain.Name, prop.Name)
					}
					if p.GTMConfig.UseTimestamp != nil && !*p.GTMConfig.UseTimestamp {
						ch <- reqsmetric
					} else {
						ch <- prometheus.NewMetricWithTimestamp(instanceTimestamp, reqsmetric)
					}
				}
				// Update summary
				propertyReqSummaryMap[domain.Name][prop.Name].Observe(float64(aggReqs))

				// Update last timestamp processed
				if instanceTimestamp.After(p.LastTimestamp[domain.Name][prop.Name]) {
					log.Debugf("Updating Last Timestamp from %v TO %v", p.LastTimestamp[domain.Name][prop.Name], instanceTimestamp)
					p.LastTimestamp[domain.Name][prop.Name] = instanceTimestamp
				}
				// only process one each interval!
				break
			} // interval end
		} // property end
	} // domain end
}

func retrievePropertyTraffic(domain, prop string, start, end time.Time) (*gtm.PropertyTrafficResponse, error) {

	qargs := make(map[string]string)
	// Get valid Traffic Window
	var err error
	propertyTrafficWindow, err := gtm.GetPropertiesTrafficWindow()
	if err != nil {
		return nil, err
	}
	// Make sure provided start and end are in range
	if propertyTrafficWindow.StartTime.Before(start) {
		if propertyTrafficWindow.EndTime.After(start) {
			qargs["start"], err = convertTimeFormat(start, time.RFC3339)
		} else {
			qargs["start"], err = convertTimeFormat(propertyTrafficWindow.EndTime, time.RFC3339)
		}
	} else {
		qargs["start"], err = convertTimeFormat(propertyTrafficWindow.StartTime, time.RFC3339)
	}
	if err != nil {
		return nil, err
	}
	if propertyTrafficWindow.EndTime.Before(end) {
		qargs["end"], err = convertTimeFormat(propertyTrafficWindow.EndTime, time.RFC3339)
	} else {
		qargs["end"], err = convertTimeFormat(end, time.RFC3339)
	}
	if err != nil {
		return nil, err
	}
	if qargs["start"] >= qargs["end"] {
		resp := &gtm.PropertyTrafficResponse{}
		resp.DataRows = make([]*gtm.PropertyTData, 0)
		log.Warnf("Start or End time outside valid report window")
		return resp, nil
	}
	resp, err := gtm.GetTrafficPerProperty(domain, prop, qargs)
	if err != nil {
		return &gtm.PropertyTrafficResponse{}, err
	}
	//DataRows is list of pointers
	sortPropertyDataRowsByTimestamp(resp.DataRows)

	return resp, nil
}
