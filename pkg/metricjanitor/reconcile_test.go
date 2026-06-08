// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// reconcileOpts returns options with a 30d unused window and a 7d grace period.
func reconcileOpts() ReconcileOptions {
	return ReconcileOptions{
		Classify: ClassifyOptions{
			UnusedFor: 30 * 24 * time.Hour,
			MinAge:    7 * 24 * time.Hour,
		},
	}
}

func reportFor(res ReconcileResult, tenant string) TenantReport {
	for _, r := range res.Reports {
		if r.Tenant == tenant {
			return r
		}
	}
	return TenantReport{}
}

func TestReconcile_FirstRunDropsNothing(t *testing.T) {
	// On the first cycle nothing has a known age, so the grace period keeps
	// everything: a brand-new controller must not drop on sight.
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	state := NewState()

	in := ReconcileInput{
		Inventory: map[string][]string{"t": {"a", "b", "c"}},
		Usage:     Usages{},
	}
	res, err := Reconcile(state, in, reconcileOpts(), now)
	require.NoError(t, err)

	rep := reportFor(res, "t")
	require.Empty(t, rep.NewlyDropped)
	require.ElementsMatch(t, []string{"a", "b", "c"}, rep.New)
	require.Nil(t, res.Overrides)
}

func TestReconcile_DropsAfterGracePeriod(t *testing.T) {
	opts := reconcileOpts()
	state := NewState()

	// First observation: establishes FirstSeen, drops nothing.
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	_, err := Reconcile(state, ReconcileInput{Inventory: map[string][]string{"t": {"unused", "used"}}}, opts, t0)
	require.NoError(t, err)

	// 10 days later (past the 7d grace period). "used" is queried recently; "unused"
	// has no usage and is now old enough to drop.
	t1 := t0.Add(10 * 24 * time.Hour)
	usage := Usages{}
	usage.ForTenant("t").RecordQueried("used", t1.Add(-time.Hour))

	res, err := Reconcile(state, ReconcileInput{
		Inventory: map[string][]string{"t": {"unused", "used"}},
		Usage:     usage,
	}, opts, t1)
	require.NoError(t, err)

	rep := reportFor(res, "t")
	require.Equal(t, []string{"unused"}, rep.NewlyDropped)
	require.Equal(t, []string{"unused"}, rep.Drop)
	require.NotNil(t, res.Overrides)
	require.Contains(t, string(res.Overrides), "unused")
	require.True(t, state.Tenant("t").Metrics["unused"].Dropped)
}

func TestReconcile_DropPersistsWhenMetricLeavesInventory(t *testing.T) {
	opts := reconcileOpts()
	state := NewState()
	ts := state.Tenant("t")
	// Pretend "gone" was dropped in a previous cycle and is no longer ingested.
	ts.markDropped("gone", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	// This cycle the inventory and usage do not mention tenant "t" at all.
	res, err := Reconcile(state, ReconcileInput{Inventory: map[string][]string{}}, opts, now)
	require.NoError(t, err)

	// The drop rule for the tenant must still be emitted.
	rep := reportFor(res, "t")
	require.Equal(t, []string{"gone"}, rep.Drop)
	require.NotNil(t, res.Overrides)
	require.Contains(t, string(res.Overrides), "gone")
}

func TestReconcile_ReinstatesQueriedDroppedMetric(t *testing.T) {
	opts := reconcileOpts()
	state := NewState()
	ts := state.Tenant("t")
	ts.markDropped("wanted", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	ts.markDropped("still_dead", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	// A user queries the dropped "wanted" metric: the query is mined even though it
	// returns no data, signaling that the metric should come back.
	usage := Usages{}
	usage.ForTenant("t").RecordQueried("wanted", now.Add(-time.Hour))

	res, err := Reconcile(state, ReconcileInput{
		Inventory: map[string][]string{"t": {}},
		Usage:     usage,
	}, opts, now)
	require.NoError(t, err)

	rep := reportFor(res, "t")
	require.Equal(t, []string{"wanted"}, rep.NewlyReinstated)
	require.Equal(t, []string{"still_dead"}, rep.Drop)
	require.False(t, state.Tenant("t").Metrics["wanted"].Dropped)
}

func TestReconcile_NoDropsClearsOverrides(t *testing.T) {
	// When nothing is dropped across all tenants, Overrides is nil so the publisher
	// writes an empty document and any prior drops are cleared.
	res, err := Reconcile(NewState(), ReconcileInput{
		Inventory: map[string][]string{"t": {"a"}},
	}, reconcileOpts(), time.Now())
	require.NoError(t, err)
	require.Nil(t, res.Overrides)
}

func TestReconcile_ForgetUnseenPrunesNonDropped(t *testing.T) {
	opts := reconcileOpts()
	opts.ForgetUnseenAfter = 14 * 24 * time.Hour
	state := NewState()
	ts := state.Tenant("t")
	ts.observe("stale", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) // very old, not dropped

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	_, err := Reconcile(state, ReconcileInput{Inventory: map[string][]string{"t": {}}}, opts, now)
	require.NoError(t, err)

	_, ok := state.Tenant("t").Metrics["stale"]
	require.False(t, ok, "stale non-dropped record should have been forgotten")
}
