// SPDX-License-Identifier: AGPL-3.0-only

package metricjanitor

import (
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// NewParser returns a PromQL parser suitable for extracting metric names from
// arbitrary user queries. Experimental functions and duration expressions are
// enabled so that queries using them still parse rather than being discarded as
// errors during mining.
func NewParser() parser.Parser {
	return parser.NewParser(parser.Options{
		EnableExperimentalFunctions: true,
		ExperimentalDurationExpr:    true,
	})
}

// ExtractMetricNames parses a PromQL expression and adds every metric name
// referenced by its vector selectors to dst. It mirrors the extraction logic
// used by "mimirtool analyze": a selector contributes its explicit name (e.g.
// `up{...}`) or, when the name is given through a matcher (e.g.
// `{__name__="up"}`), the value of that equality matcher.
//
// A non-nil error is returned only when the expression fails to parse; in that
// case dst is left unmodified. Callers mining large numbers of queries
// typically count parse errors rather than aborting. The parser p is safe to
// reuse across calls.
func ExtractMetricNames(p parser.Parser, query string, dst map[string]struct{}) error {
	expr, err := p.ParseExpr(query)
	if err != nil {
		return err
	}

	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		selector, ok := node.(*parser.VectorSelector)
		if !ok {
			return nil
		}

		// VectorSelector.Name is set when the metric name is written explicitly
		// as `name{...}`. Otherwise it may still be pinned by an equality
		// matcher on __name__.
		if selector.Name != "" {
			dst[selector.Name] = struct{}{}
			return nil
		}
		for _, m := range selector.LabelMatchers {
			if m.Name == model.MetricNameLabel && m.Type == labels.MatchEqual && m.Value != "" {
				dst[m.Value] = struct{}{}
				return nil
			}
		}
		return nil
	})

	return nil
}
