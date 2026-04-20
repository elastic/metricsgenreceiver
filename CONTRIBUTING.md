# Contributing

## Development

The Go module for this repository lives under `metricsgenreceiver`.

Common commands:

```bash
make test
```

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

6. Update the surrounding repo state with the scenario.
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
- Any allowlist entry for an all-zero double-valued family is narrow and justified.
