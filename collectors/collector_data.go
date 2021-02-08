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
	"errors"
	"github.com/prometheus/common/log"
)

const (
	HoursInDay            = 24
	trafficReportInterval = 5 // mins
)

var (
	defaultLivenessTestConfig      = LivenessTestConfig{}
	defaultTrafficDatacenterConfig = TrafficDatacenterConfig{
		Properties: make([]string, 0),
	}
	defaultTrafficPropertyConfig = TrafficPropertyConfig{
		DatacenterIDs: make([]int, 0),
		DCNicknames:   make([]string, 0),
		Targets:       make([]string, 0),
	}
	DefaultDomainTraffic = DomainTraffic{
		Properties:  make([]*TrafficPropertyConfig,0),
		Datacenters: make([]*TrafficDatacenterConfig, 0),
		Liveness:    make([]*LivenessTestConfig, 0),
	}
)

type DomainTraffic struct {
	Name        string                     `yaml:"domain_name"`
	Properties  []*TrafficPropertyConfig   `yaml:"properties,omitempty"`
	Datacenters []*TrafficDatacenterConfig `yaml:"datacenters,omitempty"`
	Liveness    []*LivenessTestConfig      `yaml:"liveness_tests,omitempty"`
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (c *DomainTraffic) UnmarshalYAML(unmarshal func(interface{}) error) error {
	*c = DefaultDomainTraffic
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
	Name          string   `yaml:"property_name"`
	DatacenterIDs []int    `yaml:"datacenter,omitempty"`
	DCNicknames   []string `yaml:"dc_nickname,omitempty"`
	Targets       []string `yaml:"target_name,omitempty"`
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
	DatacenterID int      `yaml:"datacenter_id"` // Required
	Properties   []string `yaml:"property,omitempty"`
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
	PropertyName string `yaml:"property_name"`
	AgentIP      string `yaml:"agent_ip,omitempty"`
	TargetIP     string `yaml:"target_ip,omitempty"`
	ErrorCode    bool   `yaml:"track_by_errorcode"`
	Duration     bool   `yaml:"duration_sum"`
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
	Domains       []*DomainTraffic `yaml:"domains"`
	EdgercPath    string           `yaml:"edgerc_path"`
	EdgercSection string           `yaml:"edgerc_section"`
	SummaryWindow string           `yaml:"summary_window"` // mins, hours, days, [weeks]. Default lookbackDefaultDays
	PreFillWindow string           `yaml:"prefill_window"`
	TSLabel       bool             `yaml:"timestamp_label"`             // Creates time series with traffic timestamp as label
	UseTimestamp  *bool            `yaml:"traffic_timestamp,omitempty"` // Create time series with traffic timestamp
}
