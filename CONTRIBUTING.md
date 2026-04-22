# Contributing

## Development

The Go module for this repository lives under `metricsgenreceiver`.

Common commands:

```bash
make test
```

## Core Design Principles

- Keep memory usage bounded by advancing shared templates rather than tracking per-instance or per-time-series state in memory.
- Keep scenario authoring simple by letting contributors start from the output of an actual Collector execution instead of requiring per-metric code or configuration. Optional generation hints can add more realism when needed.

Built-in scenarios live under `metricsgenreceiver/internal/metricstmpl/builtin/`.

## Built-in Scenario Guidelines

Built-in scenarios are seed data for synthetic metric generation. Treat them as representative snapshots of real telemetry, not just schema examples.

When adding or updating a scenario:

1. Start from real data.
   - Prefer a real capture or an upstream fixture over hand-written values.
   - Keep the scenario faithful to the source system's naming, labels, and resource identity.

2. Model a typical environment.
   - Avoid unusually noisy or container-heavy captures unless the scenario is explicitly meant to represent that environment.
   - Keep host shape intentional and stable. For host-level scenarios this includes CPU count, disks, mount points, and network interfaces.
   - When a related built-in already exists, prefer matching its rough host shape unless there is a reason not to.

3. Seed realistic values.
   - Initial values have a direct impact on how well generated data compresses.
   - Use realistic magnitudes and realistic decimal precision.
   - Avoid zero values in important or typically active metrics.
   - Keep zeros where zeros are genuinely common, such as error counters, disabled paths, read-only flags, or inactive interfaces.

4. Preserve numeric intent.
   - Use `asInt` for clearly integer-valued metrics.
   - Use `asDouble` for genuinely fractional metrics.
   - Keep numeric encoding consistent within a metric family.

5. Be intentional about double-valued seeds.
   - The generator infers decimal precision from initial non-zero floating point values.
   - If a double-valued metric family is seeded only with zeros, later generated values may have unrealistic precision.
   - Prefer fixing bad seeds over adding exceptions.
   - If an all-zero double-valued family is still the most realistic choice, keep the exception narrow and document why in tests.

6. Use generation hints only when the default evolution is not realistic enough.
   - Declare a hint inline in the template by adding a `metricsgen.hint.class` entry to a metric's `metadata` block. The value is the string form of a hint class (e.g. `steady_counter`). The receiver reads and strips this metadata at startup so it does not leak into emitted data.
   - Hints are supported only on number metrics (`Gauge` and `Sum`).
   - Hints are enforced per metric family within a scenario: if the same metric name appears multiple times, every occurrence must declare the same hint class or none of them may declare a hint.
   - Hints are not required and do not change the metric schema or the seeded values.
   - Hints control how metric families evolve over time on the shared template and how each instance's copy varies around it deterministically.
   - Unhinted gauges and sums receive a small default per-instance multiplier, which produces no visible variation for int metrics with small absolute values; tag those with `current_count` so instances diverge by a deterministic integer offset instead.
   - Keep hints narrow and intentional. Add them for families whose behavior materially affects realism or compression.
   - For the supported hint classes and their intended behavior, see `metricsgenreceiver/internal/distribution/generation_hints.go`.

7. Update the surrounding repo state with the scenario.
   - Add or update the matching `-resource-attributes` template.
   - Document the built-in in `README.md`.
   - Add or update receiver expectations in `metricsgenreceiver/receiver_test.go`.
   - Keep `metricsgenreceiver/internal/metricstmpl/scenario_quality_test.go` passing.
   - Keep `make test` passing.

## Scenario Review Checklist

Before merging a built-in scenario change, verify:

- The scenario is representative of a typical environment for its source.
- Important double-valued metrics have realistic non-zero seed values and decimal precision.
- Zero-heavy metrics are zero for a good reason.
- Any generation hints are narrow, understandable, and describe real behavior rather than compensating for bad seeds or violating the project's bounded-memory design principles.
- Any allowlist entry for an all-zero double-valued family is narrow and justified.
