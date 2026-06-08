// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"sort"
	"time"
)

// Reinstate returns the subset of currentlyDropped metrics that should have
// their drop rule removed because there is fresh evidence a user wants them.
//
// Once a metric is dropped at ingest it no longer appears in the inventory, so
// Classify can never see it again; reinstatement has to be driven off the
// controller's own record of what it dropped. The recovery signal comes for
// free from query mining: querying a dropped metric returns no data, but the
// query itself is still logged by the query-frontend, so MineQueryLog records it
// in Usage.LastQueried. A dropped metric that is referenced by a new
// dashboard/rule or queried within the recency window is therefore wanted again,
// and removing its drop rule lets it flow once more (going forward only — data
// dropped while the rule was active is not recoverable).
//
// The recency window is ClassifyOptions.UnusedFor; pass a ClassifyOptions with a
// shorter UnusedFor than the one used for Classify if you want reinstatement to
// react faster than metrics are condemned.
func Reinstate(currentlyDropped []string, usage *Usage, opts ClassifyOptions) []string {
	if usage == nil {
		return nil
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-opts.UnusedFor)

	var out []string
	for _, metric := range currentlyDropped {
		if isUsed(metric, usage, cutoff) {
			out = append(out, metric)
		}
	}
	sort.Strings(out)
	return out
}
