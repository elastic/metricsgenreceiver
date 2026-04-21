# AGENTS

Follow `CONTRIBUTING.md` for the canonical contributor guidance, especially for built-in scenario authoring.

Repo-specific execution notes:

- The Go module root is `metricsgenreceiver`.
- Built-in scenarios live under `metricsgenreceiver/internal/metricstmpl/builtin/`.
- When changing a built-in scenario, also check:
  - `README.md`
  - `metricsgenreceiver/receiver_test.go`
  - `metricsgenreceiver/internal/metricstmpl/scenario_quality_test.go`
- Prefer the repo-root test entry point:

```bash
make test
```
