// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMineQueryLog(t *testing.T) {
	fallback := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	queryTS := time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC)

	// A realistic mixture: a logfmt "query stats" line, a JSON line for another
	// tenant, an unrelated operational line, and a malformed PromQL query.
	log := strings.Join([]string{
		`ts=2026-06-01T09:30:00Z level=info msg="query stats" component=query-frontend user=tenant-a param_query="sum(rate(http_requests_total[5m]))"`,
		`{"ts":"2026-06-01T09:30:00Z","level":"info","msg":"query stats","user":"tenant-b","param_query":"up{job=\"api\"}"}`,
		`ts=2026-06-01T09:31:00Z level=info msg="server listening" addr=:8080`,
		`ts=2026-06-01T09:32:00Z level=info msg="query stats" user=tenant-a param_query="sum("`,
		``,
	}, "\n")

	usages := make(Usages)
	stats, err := MineQueryLog(strings.NewReader(log), usages, fallback)
	require.NoError(t, err)

	require.Equal(t, 4, stats.LinesScanned)
	require.Equal(t, 3, stats.QueriesMined)
	require.Equal(t, 1, stats.ParseErrors)

	require.Contains(t, usages, "tenant-a")
	require.Contains(t, usages, "tenant-b")

	a := usages["tenant-a"]
	require.Equal(t, map[string]time.Time{"http_requests_total": queryTS}, a.LastQueried)

	b := usages["tenant-b"]
	require.Equal(t, map[string]time.Time{"up": queryTS}, b.LastQueried)
}

func TestMineQueryLog_FallbackTimestampAndRecency(t *testing.T) {
	fallback := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	// No ts field, so the fallback timestamp must be used.
	log := `level=info msg="query stats" user=tenant-a param_query="node_load1"`

	usages := make(Usages)
	_, err := MineQueryLog(strings.NewReader(log), usages, fallback)
	require.NoError(t, err)

	require.Equal(t, fallback, usages["tenant-a"].LastQueried["node_load1"])
}

func TestMineQueryLog_KeepsMostRecentTimestamp(t *testing.T) {
	fallback := time.Now()
	log := strings.Join([]string{
		`ts=2026-06-01T09:00:00Z msg="query stats" user=t param_query="up"`,
		`ts=2026-06-03T09:00:00Z msg="query stats" user=t param_query="up"`,
		`ts=2026-06-02T09:00:00Z msg="query stats" user=t param_query="up"`,
	}, "\n")

	usages := make(Usages)
	_, err := MineQueryLog(strings.NewReader(log), usages, fallback)
	require.NoError(t, err)

	want := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	require.Equal(t, want, usages["t"].LastQueried["up"])
}
