// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"sort"
	"time"
)

// StateVersion is the schema version of a persisted State, bumped when the
// serialized layout changes incompatibly.
const StateVersion = 1

// State is the controller's complete persisted memory. The controller itself is
// stateless: every cycle it loads a State from an external store, reconciles, and
// saves it back, so that restarting or scaling the controller's pods loses no
// information. It must therefore be cheaply JSON-serializable.
type State struct {
	Version int                     `json:"version"`
	Updated time.Time               `json:"updated"`
	Tenants map[string]*TenantState `json:"tenants"`
}

// TenantState holds the per-metric memory for a single tenant.
type TenantState struct {
	Metrics map[string]*MetricRecord `json:"metrics"`
}

// MetricRecord is what the controller remembers about a single metric. FirstSeen
// powers the drop grace period; Dropped/DroppedAt track which metrics currently
// have a drop rule, which is the only way to know about them once they leave the
// inventory (a dropped metric is no longer ingested, so it cannot be rediscovered
// from the cardinality API).
type MetricRecord struct {
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Dropped   bool      `json:"dropped,omitempty"`
	DroppedAt time.Time `json:"dropped_at,omitempty"`
}

// NewState returns an empty State.
func NewState() *State {
	return &State{Version: StateVersion, Tenants: make(map[string]*TenantState)}
}

// Tenant returns the TenantState for tenant, creating an empty one on first access.
func (s *State) Tenant(tenant string) *TenantState {
	if s.Tenants == nil {
		s.Tenants = make(map[string]*TenantState)
	}
	ts, ok := s.Tenants[tenant]
	if !ok {
		ts = &TenantState{Metrics: make(map[string]*MetricRecord)}
		s.Tenants[tenant] = ts
	}
	return ts
}

func (ts *TenantState) record(metric string) *MetricRecord {
	if ts.Metrics == nil {
		ts.Metrics = make(map[string]*MetricRecord)
	}
	r, ok := ts.Metrics[metric]
	if !ok {
		r = &MetricRecord{}
		ts.Metrics[metric] = r
	}
	return r
}

// observe records that metric was present in the inventory at time now, setting
// FirstSeen the first time it is seen and always advancing LastSeen.
func (ts *TenantState) observe(metric string, now time.Time) {
	r := ts.record(metric)
	if r.FirstSeen.IsZero() {
		r.FirstSeen = now
	}
	r.LastSeen = now
}

// markDropped flips metric to dropped at time now, if it was not already.
func (ts *TenantState) markDropped(metric string, now time.Time) {
	r := ts.record(metric)
	if !r.Dropped {
		r.Dropped = true
		r.DroppedAt = now
	}
}

// markReinstated clears the dropped flag for metric.
func (ts *TenantState) markReinstated(metric string) {
	if r, ok := ts.Metrics[metric]; ok {
		r.Dropped = false
		r.DroppedAt = time.Time{}
	}
}

// firstSeenMap returns metric -> FirstSeen for use as ClassifyOptions.FirstSeen.
func (ts *TenantState) firstSeenMap() map[string]time.Time {
	out := make(map[string]time.Time, len(ts.Metrics))
	for name, r := range ts.Metrics {
		out[name] = r.FirstSeen
	}
	return out
}

// droppedMetrics returns the sorted set of metrics currently carrying a drop rule.
func (ts *TenantState) droppedMetrics() []string {
	var out []string
	for name, r := range ts.Metrics {
		if r.Dropped {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// forgetUnseen removes records for metrics that are not dropped and have not been
// seen in the inventory since cutoff, to keep state from growing without bound
// when source metrics simply stop being emitted. Dropped records are always
// retained because they are the source of truth for active drop rules.
func (ts *TenantState) forgetUnseen(cutoff time.Time) {
	for name, r := range ts.Metrics {
		if !r.Dropped && r.LastSeen.Before(cutoff) {
			delete(ts.Metrics, name)
		}
	}
}
