// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"sort"
	"time"
)

// ReconcileInput is the freshly gathered, per-tenant view of the world for one
// reconcile cycle.
type ReconcileInput struct {
	// Inventory maps tenant -> the metric names it is currently ingesting (e.g.
	// from the cardinality API). Metrics already dropped do not appear here, which
	// is why the controller relies on persisted State to remember them.
	Inventory map[string][]string
	// Usage maps tenant -> usage evidence assembled from query-log mining and
	// dashboard/rule analysis.
	Usage Usages
}

// ReconcileOptions tunes a reconcile cycle. Classify.Now and Classify.FirstSeen
// are filled in per tenant by Reconcile and may be left zero by the caller.
type ReconcileOptions struct {
	Classify ClassifyOptions
	// MaxNamesPerDropConfig is forwarded to GenerateDropConfigs.
	MaxNamesPerDropConfig int
	// ForgetUnseenAfter, when > 0, prunes non-dropped metric records not seen in
	// the inventory for this long, bounding state growth. Dropped records are
	// always retained.
	ForgetUnseenAfter time.Duration
}

// TenantReport summarizes what happened for one tenant during a reconcile cycle.
type TenantReport struct {
	Tenant string
	// Drop is the tenant's complete current drop-list after this cycle.
	Drop []string
	// NewlyDropped and NewlyReinstated are the deltas applied this cycle.
	NewlyDropped    []string
	NewlyReinstated []string
	// New are metrics kept because they are still within the MinAge grace period.
	New []string
	// Protected are metrics kept by the protection allowlist.
	Protected []string
}

// ReconcileResult is the outcome of a cycle: the rendered runtime-config
// overrides ready to publish, and a per-tenant report.
type ReconcileResult struct {
	// Overrides is the runtime-config document to publish, or nil when no tenant
	// has anything to drop.
	Overrides []byte
	Reports   []TenantReport
}

// Reconcile is the controller's pure brain. It mutates state in place to reflect
// the observations in in, decides drops and reinstatements, and renders the
// runtime-config overrides. It holds no memory of its own between calls: the
// caller is responsible for loading state from and saving it to a StateStore, so
// the controller process can be restarted or scaled freely.
func Reconcile(state *State, in ReconcileInput, opts ReconcileOptions, now time.Time) (ReconcileResult, error) {
	if state == nil {
		state = NewState()
	}

	tenants := tenantSet(in, state)
	var (
		plans   []TenantPlan
		reports = make([]TenantReport, 0, len(tenants))
	)

	for _, tenant := range tenants {
		ts := state.Tenant(tenant)
		usage := in.Usage[tenant]
		if usage == nil {
			usage = NewUsage()
		}

		// Record that every currently-ingested metric was seen now. This is what
		// establishes and ages FirstSeen for the grace period.
		inventory := in.Inventory[tenant]
		for _, metric := range inventory {
			ts.observe(metric, now)
		}

		report := TenantReport{Tenant: tenant}

		// Reinstate first: a previously dropped metric that is wanted again should
		// come back before we consider any new drops.
		reinstateOpts := opts.Classify
		reinstateOpts.Now = now
		for _, metric := range Reinstate(ts.droppedMetrics(), usage, reinstateOpts) {
			ts.markReinstated(metric)
			report.NewlyReinstated = append(report.NewlyReinstated, metric)
		}

		// Classify the currently-ingested metrics and drop the unused ones.
		classifyOpts := opts.Classify
		classifyOpts.Now = now
		classifyOpts.FirstSeen = ts.firstSeenMap()
		decision := Classify(inventory, usage, classifyOpts)
		for _, metric := range decision.Unused {
			r := ts.record(metric)
			if !r.Dropped {
				report.NewlyDropped = append(report.NewlyDropped, metric)
			}
			ts.markDropped(metric, now)
		}
		report.New = decision.New
		report.Protected = decision.Protected

		if opts.ForgetUnseenAfter > 0 {
			ts.forgetUnseen(now.Add(-opts.ForgetUnseenAfter))
		}

		report.Drop = ts.droppedMetrics()
		sort.Strings(report.NewlyDropped)
		sort.Strings(report.NewlyReinstated)
		reports = append(reports, report)

		if len(report.Drop) > 0 {
			plans = append(plans, TenantPlan{Tenant: tenant, Drop: report.Drop})
		}
	}

	state.Version = StateVersion
	state.Updated = now

	overrides, err := BuildOverrides(plans, opts.MaxNamesPerDropConfig)
	if err != nil {
		return ReconcileResult{}, err
	}
	return ReconcileResult{Overrides: overrides, Reports: reports}, nil
}

// tenantSet returns the sorted union of tenants appearing in the inventory, the
// usage, or the existing state. State tenants must be included even when absent
// from this cycle's inputs: the overrides document is regenerated wholesale, so a
// tenant whose metrics are all already dropped (and therefore not in the
// inventory) must still be re-emitted or its drop rules would be lost.
func tenantSet(in ReconcileInput, state *State) []string {
	seen := make(map[string]struct{})
	for tenant := range in.Inventory {
		seen[tenant] = struct{}{}
	}
	for tenant := range in.Usage {
		seen[tenant] = struct{}{}
	}
	for tenant := range state.Tenants {
		seen[tenant] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for tenant := range seen {
		out = append(out, tenant)
	}
	sort.Strings(out)
	return out
}
