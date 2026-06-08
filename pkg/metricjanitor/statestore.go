// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/thanos-io/objstore"
)

// StateStore persists the controller's State in external storage so that the
// controller pods can be restarted or scaled without losing memory. Implementations
// must be safe for the controller to call once per reconcile cycle.
type StateStore interface {
	// Load returns the stored State, or a fresh empty State (not an error) when no
	// state has been persisted yet.
	Load(ctx context.Context) (*State, error)
	// Save persists state, overwriting any previous value.
	Save(ctx context.Context, state *State) error
}

// BucketStateStore stores the whole State as a single JSON object in an
// object-storage bucket. Object storage is the natural external store here: it is
// durable, shared across controller replicas, and is the same backend Mimir
// already uses for blocks and runtime config.
//
// It performs no locking. Run the controller as a singleton (e.g. leader-elected)
// so that two replicas do not interleave read-modify-write cycles on the object.
type BucketStateStore struct {
	bucket objstore.Bucket
	object string
}

// NewBucketStateStore returns a StateStore that reads and writes the State at
// object within bucket.
func NewBucketStateStore(bucket objstore.Bucket, object string) *BucketStateStore {
	return &BucketStateStore{bucket: bucket, object: object}
}

// Load implements StateStore.
func (s *BucketStateStore) Load(ctx context.Context) (*State, error) {
	rc, err := s.bucket.Get(ctx, s.object)
	if err != nil {
		if s.bucket.IsObjNotFoundErr(err) {
			return NewState(), nil
		}
		return nil, fmt.Errorf("loading metricjanitor state from %q: %w", s.object, err)
	}
	defer func() { _ = rc.Close() }()

	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("reading metricjanitor state from %q: %w", s.object, err)
	}

	state := NewState()
	if err := json.Unmarshal(body, state); err != nil {
		return nil, fmt.Errorf("unmarshaling metricjanitor state from %q: %w", s.object, err)
	}
	if state.Tenants == nil {
		state.Tenants = make(map[string]*TenantState)
	}
	return state, nil
}

// Save implements StateStore.
func (s *BucketStateStore) Save(ctx context.Context, state *State) error {
	body, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling metricjanitor state: %w", err)
	}
	if err := s.bucket.Upload(ctx, s.object, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("uploading metricjanitor state to %q: %w", s.object, err)
	}
	return nil
}
