// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"strconv"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestGenerateDropConfigs(t *testing.T) {
	configs := GenerateDropConfigs([]string{"metric_b", "metric_a"}, 0)
	require.Len(t, configs, 1)

	cfg := configs[0]
	require.Equal(t, relabel.Drop, cfg.Action)
	require.Equal(t, labels.MetricName, string(cfg.SourceLabels[0]))
	// Names are sorted and the regex is anchored, so it matches exact names only.
	require.True(t, cfg.Regex.MatchString("metric_a"))
	require.True(t, cfg.Regex.MatchString("metric_b"))
	require.False(t, cfg.Regex.MatchString("metric_a_total"))
	require.False(t, cfg.Regex.MatchString("other"))
}

func TestGenerateDropConfigs_EscapesRegexMetacharacters(t *testing.T) {
	// Metric names are valid Prometheus identifiers, but a colon from a recording
	// rule plus defensive escaping must not let metacharacters leak into the regex.
	configs := GenerateDropConfigs([]string{"job:foo.bar:rate5m"}, 0)
	require.Len(t, configs, 1)
	require.True(t, configs[0].Regex.MatchString("job:foo.bar:rate5m"))
	// The '.' must be escaped, so it should not match an arbitrary character.
	require.False(t, configs[0].Regex.MatchString("job:fooXbar:rate5m"))
}

func TestGenerateDropConfigs_SplitsLargeLists(t *testing.T) {
	metrics := make([]string, 0, 250)
	for i := 0; i < 250; i++ {
		metrics = append(metrics, "metric_"+strconv.Itoa(i))
	}
	configs := GenerateDropConfigs(metrics, 100)
	require.Len(t, configs, 3) // 100 + 100 + 50
}

func TestGenerateDropConfigs_Empty(t *testing.T) {
	require.Nil(t, GenerateDropConfigs(nil, 0))
}

func TestBuildOverrides(t *testing.T) {
	plans := []TenantPlan{
		{Tenant: "tenant-a", Drop: []string{"unused_one", "unused_two"}},
		{Tenant: "tenant-empty", Drop: nil},
	}

	out, err := BuildOverrides(plans, 0)
	require.NoError(t, err)

	rendered := string(out)
	require.Contains(t, rendered, "overrides:")
	require.Contains(t, rendered, "tenant-a:")
	require.Contains(t, rendered, "metric_relabel_configs:")
	require.Contains(t, rendered, "action: drop")
	require.Contains(t, rendered, "__name__")
	// A tenant with nothing to drop must not appear.
	require.NotContains(t, rendered, "tenant-empty")

	// The rendered YAML must round-trip back into valid relabel configs.
	var parsed overridesFile
	require.NoError(t, yaml.Unmarshal(out, &parsed))
	require.Contains(t, parsed.Overrides, "tenant-a")
	require.Len(t, parsed.Overrides["tenant-a"].MetricRelabelConfigs, 1)
}

func TestBuildOverrides_NothingToDrop(t *testing.T) {
	out, err := BuildOverrides([]TenantPlan{{Tenant: "t", Drop: nil}}, 0)
	require.NoError(t, err)
	require.Nil(t, out)
}
