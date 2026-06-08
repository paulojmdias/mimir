// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import "time"

// Usage records, for a single tenant, the evidence that a metric is in use.
type Usage struct {
	// LastQueried maps a metric name to the most recent time it was observed in
	// a query. A metric only present in dashboards or rules has no entry here.
	LastQueried map[string]time.Time
	// Referenced is the set of metrics referenced statically by dashboards,
	// alerting rules or recording rules. Such metrics are always treated as used
	// regardless of query recency.
	Referenced map[string]struct{}
}

// NewUsage returns an empty Usage ready to be populated.
func NewUsage() *Usage {
	return &Usage{
		LastQueried: make(map[string]time.Time),
		Referenced:  make(map[string]struct{}),
	}
}

// RecordQueried marks metric as having been queried at ts, keeping the most
// recent timestamp seen for it.
func (u *Usage) RecordQueried(metric string, ts time.Time) {
	if metric == "" {
		return
	}
	if existing, ok := u.LastQueried[metric]; !ok || ts.After(existing) {
		u.LastQueried[metric] = ts
	}
}

// AddReferenced marks metric as referenced by a dashboard or rule.
func (u *Usage) AddReferenced(metric string) {
	if metric == "" {
		return
	}
	u.Referenced[metric] = struct{}{}
}

// Usages holds per-tenant Usage, keyed by tenant ID. Drop rules are a per-tenant
// limit in Mimir, so usage must be tracked per tenant to avoid dropping a metric
// for a tenant that still queries it.
type Usages map[string]*Usage

// ForTenant returns the Usage for tenant, creating an empty one on first access.
func (us Usages) ForTenant(tenant string) *Usage {
	u, ok := us[tenant]
	if !ok {
		u = NewUsage()
		us[tenant] = u
	}
	return u
}
