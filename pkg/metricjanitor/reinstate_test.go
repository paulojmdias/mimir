// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReinstate(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	usage := NewUsage()
	// Someone queried a dropped metric: the query is logged even though it
	// returns no data, which is exactly the "I want this back" signal.
	usage.RecordQueried("wanted_again", now.Add(-1*time.Hour))
	// Another dropped metric is now referenced by a freshly added dashboard.
	usage.AddReferenced("now_dashboarded")
	// A query that is too old should not trigger reinstatement.
	usage.RecordQueried("still_unwanted", now.Add(-40*24*time.Hour))

	dropped := []string{"wanted_again", "now_dashboarded", "still_unwanted", "never_touched"}

	got := Reinstate(dropped, usage, ClassifyOptions{
		UnusedFor: 30 * 24 * time.Hour,
		Now:       now,
	})

	require.Equal(t, []string{"now_dashboarded", "wanted_again"}, got)
}

func TestReinstate_NilUsage(t *testing.T) {
	require.Nil(t, Reinstate([]string{"a"}, nil, ClassifyOptions{UnusedFor: time.Hour}))
}

func TestReinstate_ShortWindowReactsFaster(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	usage := NewUsage()
	usage.RecordQueried("m", now.Add(-2*time.Hour))

	// A 1h window is too short to catch a 2h-old query...
	require.Empty(t, Reinstate([]string{"m"}, usage, ClassifyOptions{UnusedFor: time.Hour, Now: now}))
	// ...but a 3h window catches it.
	require.Equal(t, []string{"m"}, Reinstate([]string{"m"}, usage, ClassifyOptions{UnusedFor: 3 * time.Hour, Now: now}))
}
