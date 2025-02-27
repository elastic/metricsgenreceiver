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

Settings:
* `start_time`: the start time for the generated metrics timestamps.
* `start_now_minus`: the duration to subtract from the current time to set the start time.
  Note that when using this option, the data generation will not be deterministic.
* `end_time`: the time at which the metrics should end.
* `end_now_minus`: the duration to subtract from the current time to set the end time.
  Note that when using this option, the data generation will not be deterministic.
* `interval`: the interval at which the metrics are simulated.
  The minimum value is 1s.
* `interval_jitter` (default `false`): when enabled, adds a 0-20ms jitter to the timestamps,
  following a normal distribution with a median of 0ms and a standard deviation of 5ms.
  This simulates the real-world scenario where metrics are not perfectly aligned with the configured interval.
  When enabled, this can impact the effectiveness of the compression that a metric datastore may apply.
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
  * `concurrent_instances` (default `false`): when set to true, the receiver simulates each instance pushing metrics concurrently (in a separate goroutine) rather than sequentially.
    This can be useful to stress test the backend.
  * `path`: the path of the scenario files. Expects a `<path>.json` and a `<path>-resource-attributes.json` file.
    The `<path>.json` file contains a single batch of resource metrics in JSON format, as produced by the `fileexporter`.
    The `<path>-resource-attributes.json` file contains the resource attributes template.
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
  * `churn` (default 0): allows to simulate instances spinning down and other instances taking their place, which will create new time series.
    Time series churn may have an impact on the performance of the backend.
  * `template_vars`: the `<path>.json` file is rendered as a template.
    This option lets you specify variables that are available during template rendering.
    This allows, for example, to simulate a variable number of network devices by generating metric data points with different attributes.

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
      - path: scenarios/hostmetrics
        scale: 100

exporters:
  nop:

service:
  pipelines:
    metrics:
      receivers: [metricsgen]
      exporters: [nop]
```
