// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"regexp"
	"sort"
	"strings"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/relabel"
	"gopkg.in/yaml.v3"
)

// DefaultMaxNamesPerDropConfig bounds how many metric names are packed into a
// single relabel config regex. Splitting very large drop lists across several
// configs keeps each generated regex readable in the runtime config and avoids
// pathologically long alternations.
const DefaultMaxNamesPerDropConfig = 100

// GenerateDropConfigs builds metric_relabel_configs that drop the given metric
// names at ingest. The names are matched exactly against __name__ via an
// anchored alternation. When more than maxPerConfig names are supplied they are
// split across multiple drop configs. A maxPerConfig <= 0 falls back to
// DefaultMaxNamesPerDropConfig. Returns nil when metrics is empty.
func GenerateDropConfigs(metrics []string, maxPerConfig int) []*relabel.Config {
	if len(metrics) == 0 {
		return nil
	}
	if maxPerConfig <= 0 {
		maxPerConfig = DefaultMaxNamesPerDropConfig
	}

	// Copy and sort so output is deterministic regardless of caller ordering.
	sorted := make([]string, len(metrics))
	copy(sorted, metrics)
	sort.Strings(sorted)

	var configs []*relabel.Config
	for start := 0; start < len(sorted); start += maxPerConfig {
		end := min(start+maxPerConfig, len(sorted))
		configs = append(configs, dropConfigForNames(sorted[start:end]))
	}
	return configs
}

func dropConfigForNames(names []string) *relabel.Config {
	escaped := make([]string, len(names))
	for i, n := range names {
		escaped[i] = regexp.QuoteMeta(n)
	}
	return &relabel.Config{
		SourceLabels: model.LabelNames{model.MetricNameLabel},
		Regex:        relabel.MustNewRegexp(strings.Join(escaped, "|")),
		Action:       relabel.Drop,
	}
}

// TenantPlan is the set of metric names to drop for a single tenant.
type TenantPlan struct {
	Tenant string
	Drop   []string
}

// overridesFile mirrors the structure of a Mimir runtime-config overrides file,
// limited to the metric_relabel_configs field this controller manages.
type overridesFile struct {
	Overrides map[string]tenantOverrides `yaml:"overrides"`
}

type tenantOverrides struct {
	MetricRelabelConfigs []*relabel.Config `yaml:"metric_relabel_configs"`
}

// BuildOverrides renders the given per-tenant drop plans into a Mimir
// runtime-config overrides document. Tenants with no metrics to drop are
// omitted. maxPerConfig is forwarded to GenerateDropConfigs.
//
// The output sets metric_relabel_configs wholesale for each tenant; merging with
// relabel rules an operator already maintains is intentionally out of scope and
// left to the caller, since blindly appending could duplicate or reorder
// existing rules.
func BuildOverrides(plans []TenantPlan, maxPerConfig int) ([]byte, error) {
	out := overridesFile{Overrides: make(map[string]tenantOverrides)}
	for _, plan := range plans {
		configs := GenerateDropConfigs(plan.Drop, maxPerConfig)
		if len(configs) == 0 {
			continue
		}
		out.Overrides[plan.Tenant] = tenantOverrides{MetricRelabelConfigs: configs}
	}
	if len(out.Overrides) == 0 {
		return nil, nil
	}
	return yaml.Marshal(out)
}
