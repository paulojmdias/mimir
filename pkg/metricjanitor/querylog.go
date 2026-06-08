// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/go-logfmt/logfmt"
)

var (
	errEmptyTimestamp      = errors.New("empty timestamp")
	errUnparsableTimestamp = errors.New("unparsable timestamp")
)

// Query-frontend "query stats" and "slow query" log lines carry these fields.
// See pkg/frontend/transport/handler.go.
const (
	queryLogParamQueryField = "param_query"
	queryLogUserField       = "user"
	queryLogTimestampField  = "ts"
)

// MineStats summarizes the result of mining a query log stream.
type MineStats struct {
	// LinesScanned is the total number of non-empty lines read.
	LinesScanned int
	// QueriesMined is the number of lines that carried a param_query value.
	QueriesMined int
	// ParseErrors counts param_query values that failed to parse as PromQL.
	ParseErrors int
}

// MineQueryLog reads query-frontend logs from r and records, per tenant, the
// metrics referenced by each logged query into usages. Both logfmt (Mimir's
// default) and JSON-formatted log lines are supported. Lines without a
// param_query field (the vast majority of operational logs) are ignored.
//
// fallback is used as the query timestamp for lines that do not carry a parsable
// "ts" field; pass the current time. Mining is best-effort: a query that fails
// to parse as PromQL increments MineStats.ParseErrors but does not abort the
// scan.
func MineQueryLog(r io.Reader, usages Usages, fallback time.Time) (MineStats, error) {
	var stats MineStats

	p := NewParser()

	scanner := bufio.NewScanner(r)
	// Queries can be long; allow generous line lengths (default is 64KiB).
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		stats.LinesScanned++

		fields, ok := parseLogLine(line)
		if !ok {
			continue
		}
		query := fields[queryLogParamQueryField]
		if query == "" {
			continue
		}
		stats.QueriesMined++

		tenant := fields[queryLogUserField]
		ts := fallback
		if parsed, err := parseLogTimestamp(fields[queryLogTimestampField]); err == nil {
			ts = parsed
		}

		metrics := make(map[string]struct{})
		if err := ExtractMetricNames(p, query, metrics); err != nil {
			stats.ParseErrors++
			continue
		}
		usage := usages.ForTenant(tenant)
		for metric := range metrics {
			usage.RecordQueried(metric, ts)
		}
	}

	return stats, scanner.Err()
}

// parseLogLine extracts the key/value fields from a single log line, supporting
// both JSON objects and logfmt. The second return value is false if the line
// could not be parsed in either format.
func parseLogLine(line string) (map[string]string, bool) {
	if strings.HasPrefix(line, "{") {
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, false
		}
		fields := make(map[string]string, len(raw))
		for k, v := range raw {
			if s, ok := v.(string); ok {
				fields[k] = s
			}
		}
		return fields, true
	}

	fields := make(map[string]string)
	dec := logfmt.NewDecoder(strings.NewReader(line))
	for dec.ScanRecord() {
		for dec.ScanKeyval() {
			fields[string(dec.Key())] = string(dec.Value())
		}
	}
	if dec.Err() != nil {
		return nil, false
	}
	return fields, true
}

// parseLogTimestamp parses the timestamp formats emitted by Mimir's logger.
func parseLogTimestamp(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errEmptyTimestamp
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000000000Z07:00"} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, errUnparsableTimestamp
}
