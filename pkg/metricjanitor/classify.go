// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"sort"
	"time"

	"github.com/grafana/regexp"
)

// ClassifyOptions controls how a tenant's inventory is partitioned into used,
// unused and protected metrics.
type ClassifyOptions struct {
	// UnusedFor is the minimum time that must have elapsed since a metric was
	// last queried for it to count as unused. A metric queried more recently than
	// Now-UnusedFor is kept. This guards against dropping metrics that are only
	// queried ad-hoc and never appear in dashboards or rules.
	UnusedFor time.Duration

	// Now is the reference time against which query recency is evaluated. When
	// zero, time.Now() is used.
	Now time.Time

	// Protect lists regular expressions for metric names that must never be
	// dropped even if they look unused (e.g. SLO metrics queried only during
	// incidents, or anything matching `.*:.*` recording-rule outputs). Each
	// pattern is matched against the full metric name (anchored).
	Protect []*regexp.Regexp
}

// Decision is the result of classifying a tenant's inventory. The three slices
// are disjoint and together cover every metric in the inventory. All are sorted.
type Decision struct {
	// Used metrics are referenced by a dashboard/rule or queried recently.
	Used []string
	// Unused metrics are drop candidates.
	Unused []string
	// Protected metrics matched the protection allowlist; they are never dropped
	// and are reported separately so operators can audit what the allowlist saved.
	Protected []string
}

// Classify partitions inventory (the full set of metric names the tenant is
// currently ingesting) using the supplied usage evidence and options. A metric
// is Unused only when it is neither referenced by a dashboard/rule, nor queried
// within the UnusedFor window, nor matched by the protection allowlist.
func Classify(inventory []string, usage *Usage, opts ClassifyOptions) Decision {
	if usage == nil {
		usage = NewUsage()
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-opts.UnusedFor)

	var d Decision
	for _, metric := range inventory {
		switch {
		case matchesAny(metric, opts.Protect):
			d.Protected = append(d.Protected, metric)
		case isUsed(metric, usage, cutoff):
			d.Used = append(d.Used, metric)
		default:
			d.Unused = append(d.Unused, metric)
		}
	}

	sort.Strings(d.Used)
	sort.Strings(d.Unused)
	sort.Strings(d.Protected)
	return d
}

// isUsed reports whether metric is referenced statically or was queried at or
// after cutoff.
func isUsed(metric string, usage *Usage, cutoff time.Time) bool {
	if _, referenced := usage.Referenced[metric]; referenced {
		return true
	}
	last, queried := usage.LastQueried[metric]
	return queried && !last.Before(cutoff)
}

func matchesAny(metric string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re.MatchString(metric) {
			return true
		}
	}
	return false
}

// CompileProtectList compiles patterns into fully-anchored regexes suitable for
// ClassifyOptions.Protect, matching the anchoring semantics of Prometheus
// relabel rules so a pattern like `up` matches only the metric named exactly
// "up" rather than any metric containing it.
func CompileProtectList(patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile("^(?:" + p + ")$")
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}
