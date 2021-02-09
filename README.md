# akamai-gtm-metrics-exporter

This technical preview of the Prometheus Akamai Global Traffic Management (GTM) Metrics Exporter publishes Akamai GTM [Traffic and Liveness Report](https://developer.akamai.com/api/web_performance/global_traffic_management_reporting/v1.html) data as `up` metrics. With GTM metrics, Prometheus can track GTM property and datacenter request traffic, as well as property liveness errors. Alerts can also be triggered utilizing generated metrics; e.g. Domain datacenter requests exceeding a threshhold or the number of liveness test failures for a property exceeding a threshhold.

## Getting Started

1. Install and build the GTM exporter.
2. Configure and start the GTM Exporter to generate metrics for Prometheus.
3. Validate that the exporter target is live and metrics are available in Prometheus.

## Prerequisites

* Prometheus environment.
* [Go environment](https://golang.org/doc/install).
* Valid API client with authorization to use the Global Traffic Management Reporting API. [Akamai API Authentication](https://developer.akamai.com/getting-started/edgegrid) provides an overview and further information pertaining to the generation of authorization credentials for API based applications and tools.

## Install

```bash
go get -u github.com/akamai/akamai-gtm-metrics-exporter
```

### Docker image

A docker image can be generated by executing the following comand:

```bash
make docker
```

The resulting image is named `/akamai/akamai-gtm-metrics-exporter-linux-amd64:<git-branch>`.

## Build

```bash
make build
```

## Test

```bash
make test
```

## Configuration

The exporter requires Akamai Open Edgegrid credentials to configure the GTM API connection and can get credentials from:

1. An `.edgerc` file and section set with the exporter configuration file.
2. Environment variables.
3. Command line arguments.

### Configuration file

Configuration is usually done in file in the working directory (e.g., `./gtm_metrics_config.yml`). An example can be found in
[gtm_metrics_example_config.yml](https://github.com/akamai/akamai-gtm-metrics-exporter/gtm_metrics_example_config.yml). This configuration file may contain the following settings.

Configuration element | Description
--------------------- | -----------
domains | (Required) Akamai GTM domains to collect traffic metrics from
edgerc_path | (Optional) Accessible path to Edgegrid credentials file, e.g /home/test/.edgerc
edgerc_section | (Optional) Section in Edgegrid credentials file containing credentials
summary_window | (Optional) Rolling window for summary metric data in [m]ins, [h]ours, or [d]ays. Default: 2 days (2d)
prefill_window | (Optional) Prefill window for Report data retrieval in [m]ins, [h]ours, or [d]ays. Default: 10 minutes (10m)
timestamp_label | (Optional) Flag indicates if time series should be created with traffic timestamp as label
traffic_timestamp | (Optional) Flag indicates if time series should be created with the traffic timestamp

### Environment variables 

Authentication credentials as environment variables can exist as follows:

| Environment Variable | Description |
| -------------------- | ----------- |
| AKAMAI_HOST | Akamai Edgegrid API server |
| AKAMAI_ACCESS_TOKEN | Akamai Edgegrid API access token |
| AKAMAI_CLIENT_TOKEN |Akamai Edgegrid API client token |
| AKAMAI_CLIENT_SECRET |Akamai Edgegrid API client secret |

### Target settings

Prometheus target configuration is minimal. As the following fragment shows, settings for a static configuration for a target pointing to the exporter, the scrape interval and the scrape timeout.

```
global:
  scrape_interval: 15s
  scrape_timeout: 15s

scrape_configs:
  - job_name: 'gtm'

    static_configs:
      - targets: ['docker.for.mac.localhost:9800']
```

## Run the binary

```bash
./akamai-gtm-metrics-exporter
```

In the log, the exporter will publish a series of INFO messages to show normal operation. Look for the `Beginning to serve on address:` message to learn its port.

```
INFO[0000] Config file: gtm_metrics_config.yml           source="main.go:165"
INFO[0000] Starting GTM Metrics exporter. (version=0.1.0, branch=master, revision=99e6b08228e8772cde72818b5dcdd1b73ae633b1)  source="main.go:166"
INFO[0000] Build context: (go=go1.14.9, user=elynes@bos-lhvhpa, date=20210127-19:53:16)  source="main.go:167"
INFO[0000] akamai_gtm_metrics_exporter config loaded     source="main.go:261"
INFO[0000] GTM Metrics exporter start time: 2021-01-27 15:53:27.062040712 +0000 UTC  source="main.go:194"
INFO[0000] Beginning to serve on address :9800           source="main.go:231"
```

NOTE: running the exporter without the appropriate settings to access the GTM Traffic Reporting API will only publish build info like below. To validate, visit the exporter's metrics view with a browser using local host and the exporter's port known from one of the INFO startup messages (e.g., http://localhost:9800/metrics).

```
# HELP akamai_gtm_metrics_exporter_build_info A metric with a constant '1' value labeled by version, revision, branch, and goversion from which akamai_gtm_metrics_exporter was built.
# TYPE akamai_gtm_merics_exporter_build_info gauge
akamai_gtm_metrics_exporter_build_info{branch="master",goversion="go1.15.6",revision="84667d49203590616cd6d1b07d75715eaff31392",version="0.1.0"} 1
```

### Command line arguments

Use -h or --help flag to list available options.

```
./akamai-gtm-metrics-exporter --help
usage: akamai-gtm-metrics-exporter [<flags>]

Flags:
      -h, --help          Show context-sensitive help (also try --help-long and --help-man).
      --config.file="gtm_metrics_config.yml"
                          GTM Metrics exporter configuration file. Default: ./gtm_metrics_config.yml
      --web.listen-address=":9800"
                          The address to listen on for HTTP requests.
      --gtm.edgegrid-host=GTM.EDGEGRID-HOST
                          The Akamai Edgegrid host auth credential.
      --gtm.edgegrid-client-secret=GTM.EDGEGRID-CLIENT-SECRET
                          The Akamai Edgegrid client_secret credential.
      --gtm.edgegrid-client-token=GTM.EDGEGRID-CLIENT-TOKEN
                          The Akamai Edgegrid client_token credential.
      --gtm.edgegrid-access-token=GTM.EDGEGRID-ACCESS-TOKEN
                          The Akamai Edgegrid access_token credential.
      --log.level="info"  Only log messages with the given severity or above. Valid levels: [debug, info, warn, error, fatal]
      --log.format="logger:stderr"
                          Set the log target and format. Example: "logger:syslog?appname=bob&local=7" or "logger:stdout?json=true"
      --version           Show application version.
```

Note: By default, the exporter expects the configuration file to exist in the current working directory (e.g., `./gtm_metrics_config.yml`).

### Example invocations

`Invoke exporter with a configuration file path`

```bash
./akamai-gtm-metrics-exporter --config.file=gtm_metrics_config.yml
```

`Invoke exporter with a configuration file path and Edgegrid authentication credentials`

```bash
./akamai-gtm-metrics-exporter --config.file=gtm_metrics_example_config.yml --edgedns.edgegrid-host akab-abcdefghijklmnop-01234567890aaaaa.luna.akamaiapis.net --edgedns.edgegrid-access-token example_provided_access_token --edgedns.edgegrid-client-token example_provided_client_token --edgedns.edgegrid-client-secret example_provided_client_secret
```

## Collectors

The Akamai GTM Exporter contains collectors to gather requests traffic information for domain datacenters and properties, as well as property liveness tests failures. Each of these collectors has its own configuration, metrics and behaviors. The following sections expand on each collector's configuration, metrics and behaviors.

### Datacenter Traffic

The Datacenter collector gathers traffic data for domain datacenters.

#### Collector Configuration

An example configuration snippet for the datacenter collector is:

```
domains:
  - domain_name: testdomain.akadns.net    # domain to collect from (list)
    datacenters:
      - datacenter_id: 3131               # datacenter config from which to collect traffic metrics (list)
        property:
          - test_property                 # filter on property (list)
```

This exmple configuration instructs the collector to retrieve datacenter requests activity from datacenter 3131, property test_property. In order to retrieve requests activity for the entire datacenter, the `property` key would be omitted.

#### Collector Metrics

The datacenter collector gathers the following metrics:

Metric | Description
------ | -----------
akamai_gtm_datacenter_traffic_requests_per_interval | Number of datacenter requests per 5 minute interval (per domain)
akamai_gtm_datacenter_traffic_requests_per_interval_summary_sum | Summary aggregatio datacenter requests per 5 minute interval (per domain)
akamai_gtm_datacenter_traffic_requests_per_interval_summary_count | Summary count of datacenter requests per 5 minute interval (per domain)

The base labels used for datacenter metrics are domain and datacenter. A property label will be added if a property filter is specified. A timestamp filter will also be added if configured for the exporter.
 
The GTM Report returns datacenter requests activity aggregated in 5 minute intervals.

### Property Traffic

The Property collector gathers traffic data for domain properties.

#### Collector Configuration

An example configuration snippet for the property collector is:

```
domains:
  - domain_name: testdomain.akadns.net    # domain to collect from (list)
    properties:
      - property_name: test_property      # property config from which to collect traffic metrics (list)
        datacenter:
          - 3131                          # filter on datacenter id (list)
        dc_nickname:
          - test_nickname                 # filter on nickname (list)
        target_name:
         - test_target                    # filter on target name (list)
```

This example configuration instructs the collector to retrieve property requests activity from property test_property. The property requests can be further filtered by datacenter, nickname or target. Only the first in priority order will be used. Thus, in the example above, datacenter with id 3131. In order to retrieve requests activity for the property across all of its datacenteris, the filter keys would be omitted.

#### Collector Metrics

The property collector gathers the following metrics:

Metric | Description
------ | -----------
akamai_gtm_property_traffic_requests_per_interval | Number of property requests per 5 minute interval (per domain)
akamai_gtm_property_traffic_requests_per_interval_summary_sum | Summary aggregation of property requests per 5 minute interval (per domain) 
akamai_gtm_property_traffic_requests_per_interval_summary_count | Summary count of property requests per 5 minute interval (per domain)

The base labels used for property metrics are domain and property. An additional label (datacenterid, nickname or target) will be added if a property filter is specified. A timestamp filter will also be added if configured for the exporter.

The GTM Report returns request activity aggregated in 5 minute intervals.

### Liveness Errors

The Liveness collector gathers liveness test failure status for domain properties.

#### Collector Configuration

An example configuration snippet for the liveness collector is:

```
domains:
  - domain_name: testdomain.akadns.net    # domain to collect from (list)
    liveness_tests:
      - property_name: test_property      # property config from which to collect liveness test failures
        agent_ip: 1.2.3.4                 # filter on agent ip
        target_ip: 4.3.2.1                # filter on target ip
```

This example configuration instructs the collector to retrieve liveness test failure activity from property test_property. The liveness failures data can be further filtered by agent ip or target ip. If both are specified, target ip will be used. Thus, in the example above, the returned test failure data will be filtered for tests associated with the target ip specified. In order to retrieve all liveness test failures for the property, the filter keys would be omitted.

#### Collector Metrics

The liveness collector gathers the following metrics:

Metric | Description
------ | -----------
akamai_gtm_property_liveness_errors_datacenter_failure_duration | Datacenter falure duration (per domain, property, datacenter)
akamai_gtm_property_liveness_errors_datacenter_failures | Number of datacenter failures (per domain, property, datacenter)
akamai_gtm_property_liveness_errors_errors_per_datacenter_summary_count | Summary count of datacenter errors  (per domain and property)
akamai_gtm_property_liveness_errors_errors_per_datacenter_summary_sum | Summary aggregation of datacenter errors  (per domain and property)
akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_count | Histogram count of datacenter error duration (per domain and property)
akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_sum | Histogram aggregation of datacenter error duration (per domain and property)
akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_bucket | Histogram buckets of datacenter error duration (per domain and property)

The histogram duration buckets (in seconds) are: 60, 1800, 3600, 7200, 14400
 
The base labels used for liveness metrics are domain, property and datacenter. An additional label (targetip or agentip) will be added if a property filter is specified. A timestamp filter will also be added if configured for the exporter.

The GTM Report returns liveness test activity reflecting when tests are executed and failure status.

## Metrics Opertion

### View the metrics from the exporter's webserver

To glimpse reported GTM metric activity in the exporter, visit the exporter's metrics web page with a browser using local host and the exporter's port known from one of the INFO startup messages (e.g., http://localhost:9800/metrics). The following snippet shows console output with all three collectors configured.

```
# HELP akamai_gtm_datacenter_traffic_requests_per_interval Number of datacenter requests per 5 minute interval (per domain)
# TYPE akamai_gtm_datacenter_traffic_requests_per_interval gauge
akamai_gtm_datacenter_traffic_requests_per_interval{datacenter="3131",domain="test.akadns.net",property="testprop"} 283
# HELP akamai_gtm_datacenter_traffic_requests_per_interval_summary Number of aggregate datacenter requests per 5 minute interval (per domain)
# TYPE akamai_gtm_datacenter_traffic_requests_per_interval_summary summary
akamai_gtm_datacenter_traffic_requests_per_interval_summary_sum{datacenter="3131",domain="test.akadns.net"} 0
akamai_gtm_datacenter_traffic_requests_per_interval_summary_count{datacenter="3131",domain="test.akadns.net"} 0
# HELP akamai_gtm_metrics_exporter_build_info A metric with a constant '1' value labeled by version, revision, branch, and goversion from which akamai_gtm_metrics_exporter was built.
# TYPE akamai_gtm_metrics_exporter_build_info gauge
akamai_gtm_metrics_exporter_build_info{branch="",goversion="go1.14.9",revision="",version=""} 1
# HELP akamai_gtm_property_liveness_errors_datacenter_failure_duration Datacenter falure duration (per domain, property, datacenter)
# TYPE akamai_gtm_property_liveness_errors_datacenter_failure_duration gauge
akamai_gtm_property_liveness_errors_datacenter_failure_duration{datacenter="3201",domain="test.akadns.net",property="testprop"} 0
# HELP akamai_gtm_property_liveness_errors_datacenter_failures Number of datacenter failures (per domain, property, datacenter)
# TYPE akamai_gtm_property_liveness_errors_datacenter_failures counter
akamai_gtm_property_liveness_errors_datacenter_failures{datacenter="3201",domain="test.akadns.net",property="testprop"} 1
# HELP akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram Histogram of datacenter error duration (per domain and property)
# TYPE akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram histogram
akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_bucket{datacenter="3201",domain="test.akadns.net",property="testprop",le="60"} 3
akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_bucket{datacenter="3201",domain="test.akadns.net",property="testprop",le="1800"} 3
akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_bucket{datacenter="3201",domain="test.akadns.net",property="testprop",le="3600"} 3
akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_bucket{datacenter="3201",domain="test.akadns.net",property="testprop",le="7200"} 3
akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_bucket{datacenter="3201",domain="test.akadns.net",property="testprop",le="14400"} 3
akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_bucket{datacenter="3201",domain="test.akadns.net",property="testprop",le="+Inf"} 3
akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_sum{datacenter="3201",domain="test.akadns.net",property="testprop"} 0
akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_count{datacenter="3201",domain="test.akadns.net",property="testprop"} 3
# HELP akamai_gtm_property_liveness_errors_errors_per_datacenter_summary Summary of datacenter errors  (per domain and property)
# TYPE akamai_gtm_property_liveness_errors_errors_per_datacenter_summary summary
akamai_gtm_property_liveness_errors_errors_per_datacenter_summary_sum{datacenter="3201",domain="test.akadns.net",property="testprop"} 3
akamai_gtm_property_liveness_errors_errors_per_datacenter_summary_count{datacenter="3201",domain="test.akadns.net",property="testprop"} 3
# HELP akamai_gtm_property_traffic_requests_per_interval Number of property requests per 5 minute interval (per domain)
# TYPE akamai_gtm_property_traffic_requests_per_interval gauge
akamai_gtm_property_traffic_requests_per_interval{datacenterid="3131",domain="test.akadns.net",property="testprop"} 283
# HELP akamai_gtm_property_traffic_requests_per_interval_summary Number of aggregate property requests per 5 minute interval (per domain)
# TYPE akamai_gtm_property_traffic_requests_per_interval_summary summary
akamai_gtm_property_traffic_requests_per_interval_summary_sum{domain="test.akadns.net",property="testprop"} 0
akamai_gtm_property_traffic_requests_per_interval_summary_count{domain="test.akadns.net",property="testprop"} 0
```

### View the metrics from the prometheus webserver

To view the metrics in Prometheus, visit Graph and Execute an expression for one of the metrics. As an example, the following image shows the graph for `>>> FILL IN. Add image linki <<<`.

![Prometheus](/static/prometheus.png)

### Advanced Operation

Prometheus' default TLDB storage bounds the timestamp window that it will accept for newly created time series metrics (~2-3 hours past to current). The default Exporter configuration creates metrics with the report timestamp, prefill set to 10 minutes and a summary data size of 2 days.

The configuration paremeters used by the exporter to create metrics can be modified thru the use of advanced configuration options. Changing the defaults, though, comes with associated prometheus behavior changes. The parameters and behaviors are described in the following sections.

#### `timestamp_label` Behavior Notes

Adding a timestamp label maybe helpful in knowing the actual time and day that the event or accounting transpired in the case where creating the metric with the original timestamp is disabled.

Adding a timestamp label to each metric time series has the side effect of creating a distinct series for each label/timestamp combination. When retreiving metrics, it is recommended to use only the desired labels in the query expression. The legend displayed when viewing graphs through the Prometheus portal will contain all generated series; hundreds per day. Other viewing applications, e.g. Grafana, will allow graph customization and reduced screen clutter. 

The table tab in the Prometheus portal may provide a more manageable means to view metrics with a timestamp label. For example by only retrieving the last 5 minutes of collected metrics; e.g. `akamai_gtm_datacenter_traffic_requests_per_interval{datacenter="3131",domain="testdomain.akadns.net",property="testprop"}[5m]`

#### `traffic_timestamp` Behavior Notes

The prometheus server will reject, and not persist, the exporter's attempt to create metrics with a timestamp outside of the current time series database collection window. The Prometheus log will note a warning in this case, e.g. 

```
level=warn ts=2021-01-12T18:56:49.492Z caller=scrape.go:1378 component="scrape manager" scrape_pool=edgedns_zone target=http://localhost:9800/metrics msg="Error on ingesting samples that are too old or are too far into the future" num_dropped=2
```

and continue to collect future metric data. The dropped data will not be available for further viewing, analysis or alerting. This behavior is most likely to occur if the `prefill_window` is configured to be greater than ~2 hours.

#### `summary_window` Behavior Notes

The `summary_window` configuration informs the collector as to how much data to include when calculating requests summary count and sum. It is a rolling window, aggregating the most recent metric data.

#### `prefill_window` Behavior Notes

The `prefill_window` informs the collector as to how far to reach back in time and incorporate historical report data in prometheus. This 'priming' of the TSDB will provide a headstart in viewing and analyzing meric trends.

Configuring the prefill to be greater than the current time series open window, combined with enabling metric creation with timestamps, is that the prometheus server will reject any metrics timestampedout side the current time series window. Aside from not creating the metrics, the log will also be cluttered with warnings to this effect.

## Post Processing Metrics

Post processing of collected metrics may be designed in order to perform additional analysis of collected traffic data or to detect abnormalities in the collected data. Post processing is done on the Prometheus server. The rules executed to accomplish this post processing are specified in the prometheus server configuration file in the rules-files section. An example rules definition file, [example_gtm_metrics_alerts.rules](https://github.com/akamai/akamai-gtm-metrics-exporter/blob/master/example_gtm_metrics_alerts.rules), defines recording rules to prepare for excessive datacenter requests detection in an interval and detection of datacenter failure durations greater than 30 minutes. Snippets of the example rules file configuration that define additional metrics and the expressions to produce the metrics:

```
  - name: gtm_datacenter_requests_over_example
    rules:
      - record: instance_datacenter:akamai_gtm_datacenter_traffic_requests_per_interval:max1m
        # labels must be literals. Can't template expressions
        expr: max_over_time(akamai_gtm_datacenter_traffic_requests_per_interval{datacenter="3131",domain="test.domain.akadns.net"}[5m])
      - record: instance_datacenter:akamai_gtm_datacenter_traffic_requests_per_interval_summary:mean
        expr: |2
            akamai_gtm_datacenter_traffic_requests_per_interval_summary_sum{datacenter="3131",domain="test.domain.akadns.net"}
          /
            akamai_gtm_datacenter_traffic_requests_per_interval_summary_count{datacenter="3131",domain="test.domain.akadns.net"}
      - record: instance_datacenter:akamai_gtm_datacenter_traffic__requests_per_interval_summary:sub_mean
        expr: (instance_datacenter:akamai_gtm_datacenter_traffic_requests_per_interval:max1m - instance_datacenter:akamai_gtm_datacenter_traffic_requests_per_interval_summary*2)
	.
	.
	.
  - name: gtm_datacenter_duration_over_example
    rules:
      - record: instance_datacenter:akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_bucket:sub
        expr: scalar(akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_bucket{domain="test.domain.akadns.net", property="testprop",datacenter="3131",le="3600"}) - scalar(akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_bucket{domain="test.domain.akadns.net", property="testprop",datacenter="3131",le="1800"})
```

The first snippet identifies the largest number of datacenter requests in the five minutes, calculates the current average requests interval rate, and compares the high interval with a threshhold set as the average times 2. In this way, Prometheus records events of requests spikes indicating excessive datacenter load.

The second snippet identifies the number of test failures with a duration between 30 minutes (1800 secs) and one hour (3600 seconds). Prometheus records the number of these failures, potentially indicating excessive datacenter down down time.

These newly generated metrics can be viewed on a graph or built upon, as in this example, to detect and generate an alert.
 
## Alerting on Metrics

To detect an alert on an event or abnormality, two actions must be taken. First, an alert rule must be defined that will detect the activity of interest and generate the alert. The rules example defined in [example_gtm_metrics_alerts.rules](https://github.com/akamai/akamai-gtm-metrics-exporter/blob/master/example_gtm_metrics_alerts.rules) provides two examples of the first these to steps.

Two snippets from the rules file:

```
      - alert: DatacenterRequestsOutOfBounds
        expr: instance_datacenter:akamai_gtm_datacenter_traffic_requests_per_interval_summary:sub_mean >= 0
        labels:
          domain: "test.domain.akadns.net"
          datacenter: "3131"
          severity: critical
        annotations:
          summary: "Datacenter requests exceeded Rolling average * 2"
          description: "Job: {{ $labels.job }} Instance: {{ $labels.instance }} has Datacenter request count (current value: {{ $value }}s) compared to rolling average"
	.
	.
	.
      - alert: DatacenterExcessErrorDuration
        expr: instance_datacenter:akamai_gtm_property_liveness_errors_duration_per_datacenter_histogram_bucket:sub > 0
        labels:
          domain: "test.domain.akadns.net"
          property: "testprop"
          datacenter: "3131"
          severity: critical
        annotations:
          summary: "Datacenter test error duration exceeded 30 minutes"
          description: "Job: {{ $labels.job }} Instance: {{ $labels.instance }}"
```

present alert rules that check whether the number of interval datacenter requests exceed a threshhold and if any test durations exceeded 30 minutes..

The second step is to configure the AlertManager, e.g. the receiver of the alert, to pick up the alert (based on specified criteria) and propagate it accordingly.

[example_alertmanager_gtm_metrics.yml](https://github.com/akamai/akamai-gtm-metrics-exporter/blob/master/example_alertmanager_gtm_metrics.yml) is a simple example alertmanager configuration to receive these alerts and propagate them via email.

## Troubleshooting

Make sure the target is live and up in Prometheus Status > Targets.

>> UPDATE IMAGE and LINK <<
![Status > Targets](/static/target.png)

Make sure the service definition is correct in Prometheus Status > Service Discovery.

>> UPDATE IMAGE and LINK <<
![Status > Targets](/static/service.png)

Make sure the exporter is providing metrics to Prometheus. Visit the URL for the exporter (e.g., http://localhost:9800) and look for metrics such as the following:

```
# HELP akamai_gtm_datacenter_traffic_requests_per_interval Number of datacenter requests per 5 minute interval (per domain)
# TYPE akamai_gtm_datacenter_traffic_requests_per_interval gauge
akamai_gtm_datacenter_traffic_requests_per_interval{datacenter="3131",domain="testdomain.akadns.net",property="testprop"} 283
```

Make sure the scrape interval and timeout levels in the exporter configuration are at least 30s.

```
scrape_interval: 30s # By default, scrape targets every 15 seconds.
scrape_timeout: 30s
```

If using a docker image for the GTM exporter, Prometheus might need to explicitly reference the target appropriately.

```
    static_configs:
      - targets: ['docker.for.mac.localhost:9800']
```

## Future Work

* 

## License

Apache License 2.0, see [LICENSE](https://github.com/akamai/akamai-gtm-metrics-exporter/master/LICENSE).
