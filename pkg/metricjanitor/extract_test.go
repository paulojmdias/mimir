// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractMetricNames(t *testing.T) {
	tests := map[string]struct {
		query    string
		expected []string
		wantErr  bool
	}{
		"explicit name": {
			query:    `up`,
			expected: []string{"up"},
		},
		"name with matchers": {
			query:    `http_requests_total{job="api"}`,
			expected: []string{"http_requests_total"},
		},
		"name via __name__ matcher": {
			query:    `{__name__="node_cpu_seconds_total", mode="idle"}`,
			expected: []string{"node_cpu_seconds_total"},
		},
		"name via __name__ regex matcher is not extracted": {
			query:    `{__name__=~"node_.*"}`,
			expected: nil,
		},
		"multiple metrics in a binary expression": {
			query:    `sum(rate(http_requests_total[5m])) / sum(rate(http_requests_errors[5m]))`,
			expected: []string{"http_requests_errors", "http_requests_total"},
		},
		"deduplicated": {
			query:    `up + up`,
			expected: []string{"up"},
		},
		"parse error": {
			query:   `sum(`,
			wantErr: true,
		},
	}

	p := NewParser()
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := make(map[string]struct{})
			err := ExtractMetricNames(p, tc.query, got)
			if tc.wantErr {
				require.Error(t, err)
				require.Empty(t, got)
				return
			}
			require.NoError(t, err)
			require.ElementsMatch(t, tc.expected, keys(got))
		})
	}
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
