// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thanos-io/objstore"
)

func TestBucketStateStore_RoundTrip(t *testing.T) {
	ctx := context.Background()
	bkt := objstore.NewInMemBucket()
	store := NewBucketStateStore(bkt, "state.json")

	// Loading from an empty bucket yields a fresh state, not an error.
	loaded, err := store.Load(ctx)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Empty(t, loaded.Tenants)

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	state := NewState()
	ts := state.Tenant("tenant-a")
	ts.observe("kept_metric", now)
	ts.markDropped("dropped_metric", now)

	require.NoError(t, store.Save(ctx, state))

	reloaded, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, StateVersion, reloaded.Version)
	require.Contains(t, reloaded.Tenants, "tenant-a")
	require.Equal(t, []string{"dropped_metric"}, reloaded.Tenant("tenant-a").droppedMetrics())
	require.Equal(t, now, reloaded.Tenant("tenant-a").Metrics["kept_metric"].FirstSeen)
}
