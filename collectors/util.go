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
	configgtm "github.com/akamai/AkamaiOPEN-edgegrid-golang/configgtm-v1_4"
	edgegrid "github.com/akamai/AkamaiOPEN-edgegrid-golang/edgegrid"
	gtm "github.com/akamai/AkamaiOPEN-edgegrid-golang/reportsgtm-v1"

	"sort"
	"strings"
	"time"
)

const (
	GTMTrafficLongTimeFormat string = "2006-01-02T15:04:05Z"
	GTMTrafficDateFormat     string = "2006-01-02"
)

var (
	// EdgegridConfig contains the Akamai OPEN Edgegrid API credentials for automatic signing of requests
	EdgegridConfig edgegrid.Config = edgegrid.Config{}
	// testflag is used for test automation only
)

// Init edgegrid Config
func EdgegridInit(edgercpath, section string) error {

	config, err := edgegrid.Init(edgercpath, section)
	if err != nil {
		return fmt.Errorf("Edgegrid initialization failed. Error: %s", err.Error())
	}

	return EdgeInit(config)
}

// Finish edgegrid init
func EdgeInit(config edgegrid.Config) error {

	EdgegridConfig = config
	gtm.Init(config)
	configgtm.Init(config)

	return nil
}

// GTM Reports Query args struct
type GTMReportQueryArgs struct {
	End      string `json:"end"`   // YYYY-MM-DDThh:mm:ssZ in UTC
	Start    string `json:"start"` // YYYY-MM-DDThh:mm:ssZ in UTC
	Date     string `json:"date"`  // YYYY-MM-DD format
	AgentIP  string `json:"agentIp"`
	TargetIP string `json:"targetIp"`
}

// Liveness Errors Report Structs
type LivenessTMeta struct {
	URI      string
	Domain   string `json:"domain"`
	Property string `json:"property"`
	Date     string `json:"date"`
}

type LivenessDRow struct {
	Nickname          string `json:"nickname"`
	DatacenterID      int    `json:"datacenterId"`
	TrafficTargetName string `json:"trafficTargetName"`
	ErrorCode         int64  `json:"errorCode"`
	Duration          int64  `json:"duration"`
	TestName          string `json:"testName"`
	AgentIP           string `json:"agentIp"`
	TargetIP          string `json:"targetIp"`
}

type LivenessTData struct {
	Timestamp   string          `json:"timestamp"`
	Datacenters []*LivenessDRow `json:"datacenters"`
}

// The Liveness Errors Response structure returned by the Reports API
type LivenessErrorsResponse struct {
	Metadata    *LivenessTMeta    `json:"metadata"`
	DataRows    []*LivenessTData  `json:"dataRows"`
	DataSummary interface{}       `json:"dataSummary"`
	Links       []*configgtm.Link `json:"links"`
}

// TODO: Move to https://github.com/akamai/AkamaiOPEN-edgegrid-golang/reportsgtm-v1

// GetLivenessErrorsReport retrieves and returns a liveness errors report slice of slices with provided query filters
// See https://developer.akamai.com/api/web_performance/global_traffic_management_reporting/v1.html#getgetlivenesstestresultsforaproperty
func GetLivenessErrorsReport(domainName, propertyName string, livenessReportQueryArgs map[string]string) (*LivenessErrorsResponse, error) {

	stat := &LivenessErrorsResponse{}
	hostURL := fmt.Sprintf("/gtm-api/v1/reports/liveness-tests/domains/%s/properties/%s", domainName, propertyName)

	req, err := client.NewRequest(
		EdgegridConfig,
		"GET",
		hostURL,
		nil,
	)
	if err != nil {
		return nil, err
	}
	if _, ok := livenessReportQueryArgs["date"]; !ok {
		return nil, fmt.Errorf("GetLivenessErrorsReport: date parameter is required")
	}

	// Look for and process optional query params
	q := req.URL.Query()
	for k, v := range livenessReportQueryArgs {
		switch k {
		case "date":
			q.Add(k, v)
		case "agentIp":
			q.Add(k, v)
		case "targetIp":
			q.Add(k, v)
		}
	}
	if len(livenessReportQueryArgs) > 0 {
		req.URL.RawQuery = q.Encode()
	}

	// time stamps require urlencoded content header
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := client.Do(EdgegridConfig, req)
	if err != nil {
		return nil, err
	}

	if client.IsError(res) && res.StatusCode != 404 {
		return nil, client.NewAPIError(res)
	} else if res.StatusCode == 404 {
		cErr := configgtm.CommonError{}
		cErr.SetItem("entityName", "Liveness")
		cErr.SetItem("name", propertyName)
		return nil, cErr
	} else {
		err = client.BodyJSON(res, stat)
		if err != nil {
			return nil, err
		}

		return stat, nil
	}
}

// Util function convert time.Time to string
func convertTimeFormat(src time.Time, format string) (string, error) {
	// Make sure UTC
	t := src.UTC().Format(time.RFC3339) // "2006-01-02T15:04:05Z07:00"
	if format == time.RFC3339 {
		return t, nil
	} else if format == GTMTrafficLongTimeFormat {
		tslice := strings.Split(t, "Z")
		return tslice[0], nil
	} else if format == GTMTrafficDateFormat {
		tslice := strings.Split(t, "T")
		return tslice[0], nil
	}

	return "", fmt.Errorf("Invalid time")
}

// Create and return new GTMReportQueryArgs object
func NewGTMReportQueryArgs() *GTMReportQueryArgs {

	return &GTMReportQueryArgs{}
}

//  Util function to convert string to time.Time object
func parseTimeString(srctime, format string) (time.Time, error) {

	ts, err := time.Parse(format, srctime)

	return ts, err
}

func sortDCDataRowsByTimestamp(drs []*gtm.DCTData) {

	sort.Slice(drs, func(i, j int) bool {
		return drs[i].Timestamp < drs[j].Timestamp
	})
}

func sortPropertyDataRowsByTimestamp(drs []*gtm.PropertyTData) {

	sort.Slice(drs, func(i, j int) bool {
		return drs[i].Timestamp < drs[j].Timestamp
	})
}

func sortLivenessDataRowsByTimestamp(drs []*LivenessTData) {

	sort.Slice(drs, func(i, j int) bool {
		return drs[i].Timestamp < drs[j].Timestamp
	})
}

func stringSliceContains(sl []string, entry string) bool {

	for _, e := range sl {
		if e == entry {
			return true
		}
	}

	return false

}

func intSliceContains(sl []int, entry int) bool {

	for _, e := range sl {
		if e == entry {
			return true
		}
	}

	return false

}
