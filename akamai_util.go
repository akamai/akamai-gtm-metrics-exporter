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
	"fmt"
	gtm "github.com/akamai/AkamaiOPEN-edgegrid-golang/configgtm-v1_4"
	edgegrid "github.com/akamai/AkamaiOPEN-edgegrid-golang/edgegrid"
	reports "github.com/akamai/AkamaiOPEN-edgegrid-golang/reportsgtm-v1"
)

var (
	// edgegridConfig contains the Akamai OPEN Edgegrid API credentials for automatic signing of requests
	edgegridConfig edgegrid.Config = edgegrid.Config{}
	// testflag is used for test automation only
)

// Init edgegrid Config
func EdgegridInit(edgercpath, section string) error {

	config, err := edgegrid.Init(edgercpath, section)
	if err != nil {
		return fmt.Errorf("Edgegrid initialization failed. Error: %s", err.Error())
	}

	return edgeInit(config)
}

// Finish edgegrid init
func edgeInit(config edgegrid.Config) error {

	edgegridConfig = config
	gtm.Init(config)
	reports.Init(config)

	return nil

}
