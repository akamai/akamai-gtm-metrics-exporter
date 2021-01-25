FROM golang:1.14 as builder
WORKDIR /go/src/github.com/akamai/akamai-gtm-metrics-exporter
COPY . .
RUN make build

FROM quay.io/prometheus/busybox:latest AS app

COPY --from=builder /go/src/github.com/akamai/akamai-gtm-metrics-exporter/akamai-gtm-metrics-exporter /bin/akamai-gtm-metrics-exporter

EXPOSE 9999
ENTRYPOINT ["/bin/akamai-gtm-metrics-exporter"]
