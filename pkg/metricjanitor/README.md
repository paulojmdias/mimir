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
| Classify into used / unused / protected / new | `Classify`, `CompileProtectList` | ✅ implemented |
| Grace period for newly emitted metrics | `ClassifyOptions.MinAge` / `FirstSeen` | ✅ implemented |
| Reinstate dropped metrics on renewed use | `Reinstate` | ✅ implemented |
| Generate drop rules + runtime-config overrides | `GenerateDropConfigs`, `BuildOverrides` | ✅ implemented |
| Stateless reconcile (state in / state out) | `Reconcile` | ✅ implemented |
| External state store (survives pod restarts) | `StateStore`, `BucketStateStore` | ✅ implemented |
| Auto-apply overrides Mimir reloads | `Publisher`, `BucketPublisher` | ✅ implemented |
| Pull dashboard/rule usage from Grafana/Ruler | `pkg/mimirtool/analyze` (reuse) | ↩ available upstream |
| Pull inventory from the cardinality API | _controller wiring_ | ⬜ TODO |
| Scheduling loop + leader election | _controller wiring_ | ⬜ TODO |

## Statelessness and automated apply

The controller process holds **no durable state of its own**, so its pods can be
restarted or scaled freely:

- **State lives in object storage.** `Reconcile` is a pure function — it takes the
  previous `State` and returns the next one. Each cycle the controller does
  `state := store.Load(ctx)` → `Reconcile(...)` → `store.Save(ctx, state)` using a
  `BucketStateStore` (the same `objstore.Bucket` backend Mimir uses for blocks).
  The state remembers per-metric `FirstSeen` (for the grace period) and the
  drop-list (because a dropped metric leaves the inventory and can only be
  remembered, not rediscovered). Run the controller as a singleton / leader-elected
  so two replicas don't interleave read-modify-write on the object.

- **Mimir picks up drop rules automatically.** `-runtime-config.file` accepts a
  comma-separated list of files/URLs that Mimir **merges left to right** and
  reloads every `-runtime-config.reload-period` (10s default). Give the controller
  its own dedicated overrides file, listed **last** so it layers on top without
  clobbering operator-managed runtime config:

  ```
  -runtime-config.file=base-runtime.yaml,metricjanitor-overrides.yaml
  ```

  `BucketPublisher` writes that file each cycle; Mimir reloads it within seconds,
  no restart required. When nothing is dropped it publishes `overrides: {}` so
  clearing all drops also propagates.

```
   load State ──▶ Reconcile(state, inventory, usage) ──▶ save State
                        │
                        ├─▶ Publisher.Publish(overrides)  ─▶ Mimir auto-reloads
                        └─▶ Reports (drops / reinstatements for alerting)
```

## Usage sketch

One reconcile cycle, wiring the stateless brain to the external store and the
auto-reloaded overrides file:

```go
store := metricjanitor.NewBucketStateStore(bucket, "metricjanitor/state.json")
publisher := metricjanitor.NewBucketPublisher(bucket, "metricjanitor/overrides.yaml")
protect, _ := metricjanitor.CompileProtectList([]string{".*:.*"}) // never drop recording rules

// 1. Gather per-tenant usage: read usage from query-frontend logs...
usages := make(metricjanitor.Usages)
metricjanitor.MineQueryLog(logReader, usages, time.Now())
// ...plus dashboard/rule references from `mimirtool analyze grafana/ruler`.
usages.ForTenant("tenant-a").AddReferenced("up")

// 2. Load state, reconcile against the current inventory (cardinality API), save.
state, _ := store.Load(ctx)
res, _ := metricjanitor.Reconcile(state, metricjanitor.ReconcileInput{
    Inventory: map[string][]string{"tenant-a": inventory},
    Usage:     usages,
}, metricjanitor.ReconcileOptions{
    Classify: metricjanitor.ClassifyOptions{
        UnusedFor: 30 * 24 * time.Hour,
        MinAge:    7 * 24 * time.Hour, // grace period for new metrics
        Protect:   protect,
    },
    ForgetUnseenAfter: 30 * 24 * time.Hour,
}, time.Now())
_ = store.Save(ctx, state)

// 3. Publish the overrides; Mimir reloads them within reload-period.
_ = publisher.Publish(ctx, res.Overrides)

// res.Reports carries the per-tenant drop/reinstate deltas for alerting.
```

## Metric lifecycle

Dropping is destructive, so a metric moves through explicit states rather than
flipping straight to "dropped":

```
        observed in inventory
                │
                ▼
        ┌───────────────┐  younger than MinAge
        │      New      │◀──────────────── grace period (kept, never dropped)
        └───────┬───────┘
                │ aged past MinAge and still no usage
                ▼
        ┌───────────────┐  referenced / queried within UnusedFor
        │   Candidate   │────────────────────────────▶ Used (kept)
        └───────┬───────┘
                │ drop rule applied
                ▼
        ┌───────────────┐  queried or newly dashboarded while dropped
        │    Dropped    │────────────────────────────▶ Reinstated (rule removed)
        └───────────────┘
```

- **New → never dropped immediately.** `ClassifyOptions.MinAge` is a grace
  period: a metric must have been in the inventory at least that long before it
  can be dropped, so a freshly emitted metric is not killed before anyone has had
  a chance to dashboard or query it. With no persisted history (e.g. first run),
  unknown-age metrics are treated as New and kept — the controller drops nothing
  until it has watched a metric long enough. These surface as `Decision.New`.
- **Dropped → recoverable.** A user who wants a dropped metric back simply
  queries it. The query returns no data, but the query-frontend still logs it, so
  `MineQueryLog` records the intent and `Reinstate` flags the metric to have its
  drop rule removed (and the operator alerted). Data flows again **going
  forward** — samples dropped while the rule was active are not recoverable,
  which is the inherent trade-off of ingest-time dropping. A future
  aggregation/roll-up mode would avoid that loss.

## Safety

- **Per-tenant.** Usage, grace periods and drop rules are tracked per tenant; a
  metric used by one tenant is never dropped for another.
- **Protection allowlist.** `ClassifyOptions.Protect` (anchored regexes) pins
  metrics that are only queried during incidents, recording-rule outputs, SLO
  metrics, etc.
- **Grace period.** `ClassifyOptions.MinAge` keeps newly introduced metrics.
- **Recency window.** `UnusedFor` ensures only metrics not queried for a long
  window are dropped, not merely ones absent from dashboards.
- **Reinstatement.** `Reinstate` continuously watches for queries against dropped
  metrics and undoes the drop, so a mistake self-heals going forward.
- **Reviewable output.** `BuildOverrides` emits a runtime-config document meant
  to be inspected (e.g. committed via PR) before being applied. It deliberately
  does **not** merge with hand-maintained relabel rules.

## Roadmap

1. Controller binary: cardinality-API client for inventory, `mimirtool analyze`
   integration for dashboard/rule usage, a scheduling loop, and leader election
   (so a single replica owns the read-modify-write of the state object).
2. Additional `Publisher`/`StateStore` backends as needed (e.g. a Kubernetes
   ConfigMap publisher for clusters that mount runtime config from a ConfigMap).
3. A `--dry-run` reporting mode and a cardinality-weighted savings estimate.
4. Aggregation (roll-up) rules as a less destructive alternative to dropping,
   matching the second half of Adaptive Metrics.
