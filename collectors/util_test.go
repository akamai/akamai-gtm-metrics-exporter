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
	"github.com/stretchr/testify/assert"
	"gopkg.in/h2non/gock.v1"
	"testing"

	edgegrid "github.com/akamai/AkamaiOPEN-edgegrid-golang/edgegrid"
)

var (
	config = edgegrid.Config{
		Host:         "akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/",
		AccessToken:  "akab-access-token-xxx-xxxxxxxxxxxxxxxx",
		ClientToken:  "akab-client-token-xxx-xxxxxxxxxxxxxxxx",
		ClientSecret: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx=",
		MaxBody:      2048,
		Debug:        false,
	}
)

func TestGetTrafficReport(t *testing.T) {
	//(zone string, trafficReportQueryArgs *TrafficReportQueryArgs) (TrafficRecordsResponse, error)

	dnsTestDomain := "testdomain.com.akadns.net"
	dnsTestProperty := "testprop"
	queryargs := map[string]string{"date": "2016/11/23"}

	defer gock.Off()
	mock := gock.New(fmt.Sprintf("https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/gtm-api/v1/reports/liveness-tests/domains/%s/properties/%s", dnsTestDomain, dnsTestProperty))
	mock.
		Get(fmt.Sprintf("/gtm-api/v1/reports/liveness-tests/domains/%s/properties/%s", dnsTestDomain, dnsTestProperty)).
		HeaderPresent("Authorization").
		Reply(200).
		BodyString(`{
    			"metadata": {
        			"date": "2016-11-23",
        			"domain": "example.akadns.net",
        			"property": "www",
        		"uri": "https://akab-xxxxxxxxxxxxxxxx-xxxxxxxxxxxxxxxx.luna.akamaiapis.net/gtm-api/v1/reports/liveness-tests/domains/example.akadns.net/properties/www?date=2016-11-23"
    			},
    			"dataRows": [ {
            			"timestamp": "2016-11-23T00:13:23Z",
            			"datacenters": [ {
                    			"datacenterId": 3201,
                    			"agentIp": "204.1.136.239",
                    			"testName": "Our defences",
                    			"errorCode": 3101,
                    			"duration": 0,
                    			"nickname": "Winterfell",
                    			"trafficTargetName": "Winterfell - 1.2.3.4",
                    			"targetIp": "1.2.3.4"
                		} ]
        		} ],
    			"links": [ {
            			"rel": "self",
            			"href": "https://akab-xxxxxxxxxxxxxxxx-xxxxxxxxxxxxxxxx.luna.akamaiapis.net/gtm-api/v1/reports/liveness-tests/domains/example.akadns.net/properties/www?date=2016-11-23"
        		} ]
		}`)

	EdgeInit(config)
	// returns type TrafficRecordsResponse [][]string
	report, err := GetLivenessErrorsReport(dnsTestDomain, dnsTestProperty, queryargs)
	assert.NoError(t, err)
	assert.Equal(t, report.Metadata.Date, "2016-11-23")

}

func TestGetTrafficReport_BadArg(t *testing.T) {
	//(zone string, trafficReportQueryArgs *TrafficReportQueryArgs) (TrafficRecordsResponse, error)
	dnsTestDomain := "testdomain.com.akadns.net"
	dnsTestProperty := "testprop"
	queryargs := map[string]string{"date": "2016/11/23"}

	defer gock.Off()
	mock := gock.New(fmt.Sprintf("https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/gtm-api/v1/reports/liveness-tests/domains/%s/properties/%s", dnsTestDomain, dnsTestProperty))
	mock.
		Get(fmt.Sprintf("/gtm-api/v1/reports/liveness-tests/domains/%s/properties/%s", dnsTestDomain, dnsTestProperty)).
		HeaderPresent("Authorization").
		Reply(500).
		BodyString(`Server Error`)

	EdgeInit(config)

	// returns type TrafficRecordsResponse [][]string
	_, err := GetLivenessErrorsReport(dnsTestDomain, dnsTestProperty, queryargs)
	assert.Error(t, err)

}
