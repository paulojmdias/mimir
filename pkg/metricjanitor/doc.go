// SPDX-License-Identifier: AGPL-3.0-only

// Package metricjanitor provides the building blocks for an external controller
// that detects unused metrics and drops them at ingest by generating per-tenant
// metric_relabel_configs.
//
// The controller follows the same model as Grafana Cloud's (closed source)
// Adaptive Metrics: a metric is considered "used" if it is referenced by a
// dashboard, an alerting rule, or a recording rule, OR if it has been queried
// recently. Everything else in the tenant's series inventory is a candidate for
// dropping.
//
// The package is deliberately free of any network or Mimir client code so that
// the classification logic is unit-testable in isolation. The intended data
// flow is:
//
//		dashboards + rules ─┐
//		query-frontend logs ─┼─▶ Usage ─┐
//		                                 ├─▶ Classify ─▶ Decision ─▶ BuildOverrides ─▶ runtime config
//		cardinality API (inventory) ─────┘
//
//	  - Usage is assembled from ExtractMetricNames (dashboards/rules) and
//	    MineQueryLog (read usage from query-frontend "query stats" logs).
//	  - Classify compares the tenant's full inventory against its Usage and an
//	    operator-provided protection allowlist.
//	  - BuildOverrides renders the unused metrics into a Mimir runtime-config
//	    overrides file containing drop relabel rules.
package metricjanitor
