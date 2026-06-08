// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thanos-io/objstore"
)

func TestBucketPublisher(t *testing.T) {
	ctx := context.Background()
	bkt := objstore.NewInMemBucket()
	pub := NewBucketPublisher(bkt, "overrides.yaml")

	// Publishing real overrides writes them verbatim.
	require.NoError(t, pub.Publish(ctx, []byte("overrides:\n  t:\n    metric_relabel_configs: []\n")))
	require.Contains(t, readObject(t, bkt, "overrides.yaml"), "metric_relabel_configs")

	// Publishing nothing writes a valid empty document so prior drops are cleared.
	require.NoError(t, pub.Publish(ctx, nil))
	require.Equal(t, "overrides: {}\n", readObject(t, bkt, "overrides.yaml"))
}

func readObject(t *testing.T, bkt objstore.Bucket, name string) string {
	t.Helper()
	rc, err := bkt.Get(context.Background(), name)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(body)
}
