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
	"fmt"
	//"github.com/akamai/AkamaiOPEN-edgegrid-golang/client-v1"
	gtm "github.com/akamai/AkamaiOPEN-edgegrid-golang/reportsgtm-v1"
	//"strconv"
	"sort"
	"strings"
	"time"
)

const (
	GTMTrafficLongTimeFormat string = "2006-01-02T15:04:05Z"
	GTMTrafficDateFormat     string = "2006-01-02"
)

var (
	// testflag is used for test automation only
	testflag bool = false
)

// GTM Reports Query args struct
type GTMReportQueryArgs struct {
	End      string `json:"end"`   // YYYY-MM-DDThh:mm:ssZ in UTC
	Start    string `json:"start"` // YYYY-MM-DDThh:mm:ssZ in UTC
	Date     string `json:"date"`  // YYYY-MM-DD format
	AgentIp  string `json:"agentIp"`
	TargetIp string `json:"targetIp"`
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
