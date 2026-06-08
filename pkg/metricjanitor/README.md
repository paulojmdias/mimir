# metricjanitor

`metricjanitor` is the core library for an **external controller** that detects
unused metrics and drops them at ingest by generating per-tenant
`metric_relabel_configs` for Mimir's runtime configuration.

It is the open-source-buildable counterpart to the detection-and-drop half of
Grafana Cloud's (closed source) [Adaptive Metrics][adaptive]. It reuses the same
"used metric" definition: a metric is **used** if it is referenced by a
dashboard, an alerting rule, or a recording rule, **or** if it has been queried
recently. Everything else in a tenant's series inventory is a drop candidate.

[adaptive]: https://grafana.com/docs/grafana-cloud/adaptive-telemetry/adaptive-metrics/

## Why this exists

Mimir already ships the pieces to *act* on unused metrics
(`metric_relabel_configs`, the cardinality API, `mimirtool analyze`), but nothing
open source closes the loop by automatically deciding *what* is unused. Two gaps
in particular shaped this design:

- **Read usage is not tracked natively.** Mimir's query stats record series and
  chunk counts, not which metric *names* were queried. We recover that signal by
  mining the query-frontend `query stats` log lines (`param_query`).
- **Retention is tenant-level only.** There is no per-metric TTL, so "reduce
  retention of unused metrics" is not directly expressible. Dropping (or, later,
  aggregating) at ingest is the realistic lever, which is what this controller
  does.

## Data flow

```
dashboards + rules ─┐  (ExtractMetricNames)
query-frontend logs ─┼─▶ Usage ─┐
                                 ├─▶ Classify ─▶ Decision ─▶ BuildOverrides ─▶ runtime-config overrides
cardinality API (inventory) ─────┘
```

| Stage | Entry point | Status |
|-------|-------------|--------|
| Extract metric names from PromQL | `ExtractMetricNames` | ✅ implemented |
| Mine read usage from query-frontend logs | `MineQueryLog` | ✅ implemented |
| Track per-tenant usage | `Usage`, `Usages` | ✅ implemented |
| Classify inventory into used / unused / protected | `Classify`, `CompileProtectList` | ✅ implemented |
| Generate drop rules + runtime-config overrides | `GenerateDropConfigs`, `BuildOverrides` | ✅ implemented |
| Pull dashboard/rule usage from Grafana/Ruler | `pkg/mimirtool/analyze` (reuse) | ↩ available upstream |
| Pull inventory from the cardinality API | _controller wiring_ | ⬜ TODO |
| Apply overrides + scheduling loop | _controller wiring_ | ⬜ TODO |

## Usage sketch

```go
usages := make(metricjanitor.Usages)

// 1. Read usage: mine query-frontend logs.
metricjanitor.MineQueryLog(logReader, usages, time.Now())

// 2. Static usage: feed metrics from `mimirtool analyze grafana/ruler`.
u := usages.ForTenant("tenant-a")
for _, m := range dashboardAndRuleMetrics {
    u.AddReferenced(m)
}

// 3. Classify the tenant's full inventory (from the cardinality API).
protect, _ := metricjanitor.CompileProtectList([]string{".*:.*"}) // never drop recording rules
decision := metricjanitor.Classify(inventory, usages.ForTenant("tenant-a"), metricjanitor.ClassifyOptions{
    UnusedFor: 30 * 24 * time.Hour,
    Protect:   protect,
})

// 4. Render drop rules into a runtime-config overrides file.
out, _ := metricjanitor.BuildOverrides([]metricjanitor.TenantPlan{
    {Tenant: "tenant-a", Drop: decision.Unused},
}, metricjanitor.DefaultMaxNamesPerDropConfig)
```

## Safety

Dropping data is destructive and irreversible, so the design favors caution:

- **Per-tenant.** Usage and drop rules are tracked per tenant; a metric used by
  one tenant is never dropped for another.
- **Protection allowlist.** `ClassifyOptions.Protect` (anchored regexes) keeps
  metrics that are only queried during incidents, recording-rule outputs, SLO
  metrics, etc.
- **Recency window.** `UnusedFor` ensures only metrics not queried for a long
  window are dropped, not merely ones absent from dashboards.
- **Reviewable output.** `BuildOverrides` emits a runtime-config document meant
  to be inspected (e.g. committed via PR) before being applied. It deliberately
  does **not** merge with hand-maintained relabel rules.

## Roadmap

1. Controller wiring: cardinality-API client for inventory, `mimirtool analyze`
   integration for dashboard/rule usage, and an apply step (write runtime
   config, or open a PR).
2. A `--dry-run` reporting mode and a cardinality-weighted savings estimate.
3. Aggregation (roll-up) rules as a less destructive alternative to dropping,
   matching the second half of Adaptive Metrics.
