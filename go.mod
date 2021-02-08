module github.com/akamai/akamai-gtm-metrics-exporter

go 1.14

require (
	github.com/akamai/AkamaiOPEN-edgegrid-golang v1.0.1
	github.com/prometheus/client_golang v1.7.1
	github.com/prometheus/common v0.15.0
	github.com/stretchr/testify v1.4.0
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	gopkg.in/h2non/gock.v1 v1.0.15
	gopkg.in/yaml.v2 v2.4.0
)

//replace github.com/akamai/akamai-gtm-metrics-exporter => /home/github.com/akamai/akamai-gtm-metrics-exporter
