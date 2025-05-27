# Metrics generation receiver

| Status        |                          |
| ------------- |--------------------------|
| Stability     | development: metrics     |

Generates metrics given an initial OTLP JSON file that was produced with the `fileexporter`.
This receiver is inspired by the metrics dataset generation tool that's part of https://github.com/timescale/tsbs.
The difference is that this makes it easier to use real-ish OTel metrics from a receiver such as the `hostmetricsreceiver`
and send it to different backends using the corresponding exporter.

Given an initial set of resource metrics, this receiver generates metrics with a configurable scale, start time, end time, and interval.
For example, given the output of a single report from the `hostmetricsreceiver`,
lets you generate a day's worth of data from multiple simulated hosts with a given interval.

The datapoints for the metrics are individually simulated using a distribution that can be customized with the `distribution` settings.

## Getting Started

### Quick Start

To get started quickly, [check out the releases page](https://github.com/elastic/metricsgenreceiver/releases) for compiled binaries you can use to get started right away. 

### Building

metricsgenreceiver is a receiver for the otel collector. To build the otelcollector, the tool
[ocb](https://opentelemetry.io/docs/collector/custom-collector/) is needed. To install it on OS X, run

```bash
curl --proto '=https' --tlsv1.2 -fL -o ocb \
https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/cmd%2Fbuilder%2Fv0.123.0/ocb_0.123.0_darwin_arm64
chmod +x ocb
```

Be aware, currently this exact version is needed (0.123.0).

Then run the following command:

```bash
./ocb --config builder-config.yaml
```

This will build the otel collector binary under `./otelcol-dev/otelcol`.

You can start the Otel Collector with the predefined config:

```
./otelcol-dev/otelcol --config otelcol.yaml
```

For local development, you can use the `otelcol.dev.yaml` config file which is ignored by default.


### Receiver Settings

It is possible to adjust the metricsgen receiver with the configs listed below to enable different scenarios. To do this, adjust the section below:

```
receivers:
  metricsgen:
    ...
```

* `start_time`: the start time of the generated metrics (inclusive).
* `start_now_minus`: the duration to subtract from the current time to set the start time.
  Note that when using this option, the data generation will not be deterministic.
* `end_time`: the end time of the generated metrics (exclusive).
* `end_now_minus`: the duration to subtract from the current time to set the end time.
  Note that when using this option, the data generation will not be deterministic.
* `interval`: the interval at which the metrics are simulated.
  The minimum value is 1s.
* `interval_jitter_std_dev` (default `0`): when set to a non-zero value (such as `5ms`), a jitter is added to the interval.
  The jitter is equal to the absolute value of a normal distribution with a median of 0ms and a standard deviation of the specified value.
  It is capped by the interval, so that the next interval will never start at or before the previous one.
  This simulates real-world scenarios where the collection interval is sometimes a bit late due to delays running the scheduled metric collection.
  When set to a non-zero value, this can impact the effectiveness of the compression that a metric datastore may apply.
* `real_time` (default `false`): by default, the receiver generates the metrics as fast as possible.
  When set to true, it will pause after each cycle according to the configured `interval`.
* `exit_after_end` (default `false`): when set to true, will terminate the collector.
* `seed` (default `0`): seed value for the random number generator that's used for simulating the standard distribution. The seed makes sure that the data generation is deterministic.
* `distribution`: the datapoints for the metrics are individually simulated by advancing their initial value using a standard distribution,
  taking into account the [temporality](https://opentelemetry.io/docs/specs/otel/metrics/data-model/#temporality),
  [monotonicity](https://opentelemetry.io/docs/specs/otel/metrics/data-model/#point-kinds),
  and capping floating point gauges values whose initial value is between 0 and 1 to that range.
  By changing the distribution parameters, you can simulate how predictable the metrics are evolving over time.
  This may have an impact on how well the backend can compress the data.
  * `median_monotonic_sum` (default `100`): the median value for the normal distribution used to simulate metrics that are monotonic sums.
    If the sum is not monotonic, or if the metric is a gauge, the median will always be zero, to simulate a value that goes up and down over time.
    If the temporality of the metric is delta, the new value will be set to the result of the normal distribution.
    Otherwise (if the metric is using cumulative or unspecified temporality), the new value will be incremented by the result of the normal distribution.
  * `std_dev_gauge_pct` (default `0.05`): the standard deviation used for floating point gauge metrics whose initial value is between 0 and 1.
  * `std_dev` (default `5`): the standard deviation of the normal distribution used to simulate all other metrics.
    The generated metrics will be kept within that range and the median is always 0, to simulate a value that goes up and down over time.
    The new value will be incremented by the result of the normal distribution.
* `scenarios`: a list of scenarios to simulate. For every interval, each scenario is simulated before moving to the next interval.
  * `scale`: determines how many instances (like hosts) to simulate.
    The individual instances will a have a consistent set of resource attributes throughout the simulation.
  * `concurrency` (default `0`): when set to a non-zero value, the receiver will simulate the scenario concurrently with the specified number of goroutines.
  * `path`: the path of the scenario files.
    * Built-in scenarios:
      This receiver comes with a number of pre-packaged scenarios:
      * `builtin/hostmetrics`: simulates metrics from the `hostmetricsreceiver`.
      * `builtin/kubeletstats-node`: simulates node metrics from the `kubeletstatsreceiver`.
      * `builtin/kubeletstats-pod`: simulates pod metrics from the `kubeletstatsreceiver`.
        Each simulated pod reports metrics for the pod itself, one container, and one volume mount.
      * `builtin/tsbs-devops`: an adaptation of the [Timescale TSBS](https://github.com/timescale/tsbs) devops scenario.
      * `builtin/elasticapm-service-metrics`: simulates aggregated service metrics from the `elasticapmconnector`. The scale influences how many services are simulated.
      * `builtin/elasticapm-span-destination-metrics`: simulates aggregated span destination metrics from the `elasticapmconnector`. The scale influences how many services are simulated.Uses the `template_vars` option to customize the data generation:
        * `destinations`: the number of distinct exit span names to simulate per service. This affects the number of simulated metrics.
      * `builtin/elasticapm-transaction-metrics`: simulates aggregated transaction metrics from the `elasticapmconnector`. The scale influences how many service instances are simulated. Uses the `template_vars` option to customize the data generation:
        * `services`: the number of services to simulate. Does not affect the number of metrics.
        * `transactions`: the number of transactions to simulate per service instance. This affects the number of simulated metrics.
    * `builtin/nginx`: Built in scenario for nginx connection metrics simulating the `nginxreceiver`.
    * Custom scenarios:
      Expects a `<path>.json` or `<path>.yaml` and a `<path>-resource-attributes.json` or `<path>-resource-attributes.yaml` file.
      The `<path>.json`/`<path>.yaml` file contains a single batch of resource metrics in JSON or YAML format, as produced by the `fileexporter`.
      The `<path>-resource-attributes.json`/`<path>-resource-attributes.yaml` file contains the resource attributes template.
      The resource attributes template is used to simulate the individual instances.
      These resource attributes are injected into all resource metrics for which a matching resource attribute key exists.
      Supported placeholders:
        * `{{.InstanceID}}` (an integer equal to the number of the simulated instance, starting with `0`)
        * `{{.RandomIPv4}}`
        * `{{.RandomIPv6}}`
        * `{{.RandomMAC}}`
        * `{{.RandomHex <length>}}`
        * `{{.UUID}}`
        * `{{.InstanceStartTime}}`
        * `{{.RandomFrom <list>}}` (a random element from the list)
        * `{{.ModFrom <n> <list>}}` (an element from the list at index `n % len(list)`)
        * `{{.RandomIntn <n>}}` (a random integer in the range `[0, n)`)
        * `{{.Mod <x> <y>}}` (the result of `x % y`)
  * `churn` (default 0): allows to simulate instances spinning down and other instances taking their place, which will create new time series.
    Time series churn may have an impact on the performance of the backend.
  * `template_vars`: the `<path>.json`/`<path>.yaml` file is rendered as a template.
    This option lets you specify variables that are available during template rendering.
    This allows, for example, to simulate a variable number of network devices by generating metric data points with different attributes.
  * `temporality_override`: allows to override the temporality of the metrics in the scenario.
    Supported values: `cumulative`, `delta`.
    This can be used to accommodate backends that only support a specific temporality.

Example configuration:
```yaml
receivers:
  metricsgen:
    start_time: "2025-01-01T00:00:00Z"
    end_time: "2025-01-01T01:00:00Z"
    interval: 10s
    exit_after_end: true
    seed: 123
    scenarios:
      - path: builtin/hostmetrics
        scale: 100

exporters:
  nop:

service:
  pipelines:
    metrics:
      receivers: [metricsgen]
      exporters: [nop]
```

### Exporter settings

Multiple exporter settings are already in the default config. Adjust the outputs needed. In case the metricsgenreceiver is sending data to a stack setup with elastic-package, the following exporter config
for Elasticsearch has to be used (assuming it runs on localhost):

```
exporters:
  elasticsearch:
    endpoint: "https://localhost:9200"
    mapping:
      mode: otel
    metrics_dynamic_index:
      enabled: true
    num_workers: 10
    user: elastic
    password: changeme
    tls:
      insecure_skip_verify: true
```
