// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"bytes"
	"context"
	"fmt"

	"github.com/thanos-io/objstore"
)

// Publisher delivers the rendered runtime-config overrides to wherever Mimir
// reads its runtime configuration, so that drop rules are picked up automatically
// without restarting Mimir.
//
// The intended wiring exploits Mimir's -runtime-config.file accepting a
// comma-separated list of files/URLs that are merged left to right and reloaded
// every -runtime-config.reload-period (10s by default). The controller owns one
// dedicated overrides file listed last in that list, so its output is layered on
// top of operator-managed runtime config without ever clobbering it.
type Publisher interface {
	// Publish writes overrides as the controller's runtime-config file. A nil or
	// empty overrides means "nothing to drop"; implementations should publish an
	// empty-but-valid document so that previously published drop rules are cleared.
	Publish(ctx context.Context, overrides []byte) error
}

// emptyOverrides is a valid runtime-config document declaring no overrides. It is
// published when there is nothing to drop so that clearing all drops propagates
// to Mimir rather than leaving a stale file in place.
var emptyOverrides = []byte("overrides: {}\n")

// BucketPublisher writes the overrides as a single object in an object-storage
// bucket. Point Mimir's -runtime-config.file at the same object (via a bucket
// sync sidecar, or directly if the deployment fronts the bucket over HTTP) so the
// reload loop picks it up.
type BucketPublisher struct {
	bucket objstore.Bucket
	object string
}

// NewBucketPublisher returns a Publisher that writes overrides to object within
// bucket.
func NewBucketPublisher(bucket objstore.Bucket, object string) *BucketPublisher {
	return &BucketPublisher{bucket: bucket, object: object}
}

// Publish implements Publisher.
func (p *BucketPublisher) Publish(ctx context.Context, overrides []byte) error {
	if len(overrides) == 0 {
		overrides = emptyOverrides
	}
	if err := p.bucket.Upload(ctx, p.object, bytes.NewReader(overrides)); err != nil {
		return fmt.Errorf("publishing metricjanitor overrides to %q: %w", p.object, err)
	}
	return nil
}
