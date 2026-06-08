// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClassify(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	usage := NewUsage()
	usage.AddReferenced("dashboard_metric")
	usage.RecordQueried("recently_queried", now.Add(-24*time.Hour))
	usage.RecordQueried("stale_metric", now.Add(-40*24*time.Hour))

	inventory := []string{
		"dashboard_metric",  // referenced -> used
		"recently_queried",  // queried within window -> used
		"stale_metric",      // queried long ago -> unused
		"never_seen",        // no evidence -> unused
		"slo:request:ratio", // protected by allowlist
	}

	protect, err := CompileProtectList([]string{".*:.*"})
	require.NoError(t, err)

	got := Classify(inventory, usage, ClassifyOptions{
		UnusedFor: 30 * 24 * time.Hour,
		Now:       now,
		Protect:   protect,
	})

	require.Equal(t, []string{"dashboard_metric", "recently_queried"}, got.Used)
	require.Equal(t, []string{"never_seen", "stale_metric"}, got.Unused)
	require.Equal(t, []string{"slo:request:ratio"}, got.Protected)
}

func TestClassify_NilUsageMarksEverythingUnused(t *testing.T) {
	inventory := []string{"b", "a", "c"}
	got := Classify(inventory, nil, ClassifyOptions{UnusedFor: time.Hour})
	require.Equal(t, []string{"a", "b", "c"}, got.Unused)
	require.Empty(t, got.Used)
	require.Empty(t, got.Protected)
}

func TestClassify_QueryExactlyAtCutoffIsUsed(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	usage := NewUsage()
	usage.RecordQueried("edge", now.Add(-30*24*time.Hour))

	got := Classify([]string{"edge"}, usage, ClassifyOptions{
		UnusedFor: 30 * 24 * time.Hour,
		Now:       now,
	})
	require.Equal(t, []string{"edge"}, got.Used)
}

func TestClassify_GracePeriodKeepsNewMetrics(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	inventory := []string{"old_unused", "freshly_added", "age_unknown"}
	firstSeen := map[string]time.Time{
		"old_unused":    now.Add(-10 * 24 * time.Hour), // older than MinAge -> droppable
		"freshly_added": now.Add(-12 * time.Hour),      // younger than MinAge -> kept as New
		// "age_unknown" intentionally absent -> treated as too new -> kept as New.
	}

	got := Classify(inventory, NewUsage(), ClassifyOptions{
		UnusedFor: 30 * 24 * time.Hour,
		Now:       now,
		MinAge:    7 * 24 * time.Hour,
		FirstSeen: firstSeen,
	})

	require.Equal(t, []string{"old_unused"}, got.Unused)
	require.Equal(t, []string{"age_unknown", "freshly_added"}, got.New)
	require.Empty(t, got.Used)
}

func TestClassify_GracePeriodDisabledByDefault(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	// With MinAge == 0 the grace period is off, so age and FirstSeen are ignored.
	got := Classify([]string{"brand_new"}, NewUsage(), ClassifyOptions{
		UnusedFor: time.Hour,
		Now:       now,
		FirstSeen: map[string]time.Time{"brand_new": now},
	})
	require.Equal(t, []string{"brand_new"}, got.Unused)
	require.Empty(t, got.New)
}

func TestCompileProtectList_AnchorsPatterns(t *testing.T) {
	protect, err := CompileProtectList([]string{"up"})
	require.NoError(t, err)
	require.True(t, protect[0].MatchString("up"))
	require.False(t, protect[0].MatchString("up_total"))
	require.False(t, protect[0].MatchString("node_up"))
}
