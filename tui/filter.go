package tui

import (
	"regexp"
	"strings"
	"time"

	tapv1 "github.com/mickamy/sql-tap/gen/tap/v1"
	"github.com/mickamy/sql-tap/proxy"
)

type filterKind int

const (
	filterText     filterKind = iota // plain text substring match
	filterDuration                   // d>100ms, d<10ms
	filterError                      // "error" keyword
	filterOp                         // op:select, op:begin, etc.
)

type durationOp int

const (
	durGT durationOp = iota // >
	durLT                   // <
)

type filterCondition struct {
	kind filterKind

	// filterText
	text string

	// filterDuration
	durOp    durationOp
	durValue time.Duration

	// filterOp — matched against proxy.Op name or SQL keyword prefix
	opPattern string
}

var reDuration = regexp.MustCompile(`^d([><])(\d+(?:\.\d+)?)(us|µs|ms|s|m)$`)

// sqlOpKeywords maps SQL keyword prefixes to proxy.Op values for op:select style filters.
var sqlOpKeywords = map[string][]proxy.Op{
	"select": {proxy.OpQuery, proxy.OpExec, proxy.OpExecute},
	"insert": {proxy.OpQuery, proxy.OpExec, proxy.OpExecute},
	"update": {proxy.OpQuery, proxy.OpExec, proxy.OpExecute},
	"delete": {proxy.OpQuery, proxy.OpExec, proxy.OpExecute},
}

// protocolOps maps protocol operation names to proxy.Op values.
var protocolOps = map[string]proxy.Op{
	"query":    proxy.OpQuery,
	"exec":     proxy.OpExec,
	"prepare":  proxy.OpPrepare,
	"bind":     proxy.OpBind,
	"execute":  proxy.OpExecute,
	"begin":    proxy.OpBegin,
	"commit":   proxy.OpCommit,
	"rollback": proxy.OpRollback,
}

func parseFilter(input string) []filterCondition {
	tokens := strings.Fields(input)
	conds := make([]filterCondition, 0, len(tokens))

	for _, tok := range tokens {
		if c, ok := parseDuration(tok); ok {
			conds = append(conds, c)
			continue
		}
		if strings.ToLower(tok) == "error" {
			conds = append(conds, filterCondition{kind: filterError})
			continue
		}
		if c, ok := parseOp(tok); ok {
			conds = append(conds, c)
			continue
		}
		// Fallback: plain text match.
		conds = append(conds, filterCondition{
			kind: filterText,
			text: strings.ToLower(tok),
		})
	}
	return conds
}

func parseDuration(tok string) (filterCondition, bool) {
	m := reDuration.FindStringSubmatch(tok)
	if m == nil {
		return filterCondition{}, false
	}
	op := durGT
	if m[1] == "<" {
		op = durLT
	}
	unit := m[3]
	// Parse the numeric part manually to keep it simple.
	raw := m[2] + unitSuffix(unit)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return filterCondition{}, false
	}
	return filterCondition{
		kind:     filterDuration,
		durOp:    op,
		durValue: d,
	}, true
}

func unitSuffix(unit string) string {
	switch unit {
	case "us", "µs":
		return "us"
	case "ms":
		return "ms"
	case "s":
		return "s"
	case "m":
		return "m"
	}
	return "ms"
}

func parseOp(tok string) (filterCondition, bool) {
	lower := strings.ToLower(tok)
	if !strings.HasPrefix(lower, "op:") {
		return filterCondition{}, false
	}
	pattern := lower[3:]
	if pattern == "" {
		return filterCondition{}, false
	}
	return filterCondition{
		kind:      filterOp,
		opPattern: pattern,
	}, true
}

func (c filterCondition) matchesEvent(ev *tapv1.QueryEvent) bool {
	switch c.kind {
	case filterText:
		return strings.Contains(strings.ToLower(ev.GetQuery()), c.text)
	case filterDuration:
		d := ev.GetDuration()
		if d == nil {
			return false
		}
		dur := d.AsDuration()
		switch c.durOp {
		case durGT:
			return dur > c.durValue
		case durLT:
			return dur < c.durValue
		}
	case filterError:
		return ev.GetError() != ""
	case filterOp:
		return matchOp(ev, c.opPattern)
	}
	return false
}

func matchOp(ev *tapv1.QueryEvent, pattern string) bool {
	// Check protocol-level op match (begin, commit, rollback, query, exec, etc.)
	if op, ok := protocolOps[pattern]; ok {
		return proxy.Op(ev.GetOp()) == op
	}
	// Check SQL keyword prefix match (select, insert, update, delete).
	if _, ok := sqlOpKeywords[pattern]; ok {
		q := strings.TrimSpace(strings.ToLower(ev.GetQuery()))
		return strings.HasPrefix(q, pattern)
	}
	return false
}

func matchAllConditions(ev *tapv1.QueryEvent, conds []filterCondition) bool {
	for _, c := range conds {
		if !c.matchesEvent(ev) {
			return false
		}
	}
	return true
}

func describeFilter(input string) string {
	conds := parseFilter(input)
	if len(conds) == 0 {
		return input
	}
	var parts []string
	for _, c := range conds {
		switch c.kind {
		case filterText:
			parts = append(parts, "text:"+c.text)
		case filterDuration:
			op := ">"
			if c.durOp == durLT {
				op = "<"
			}
			parts = append(parts, "d"+op+c.durValue.String())
		case filterError:
			parts = append(parts, "error")
		case filterOp:
			parts = append(parts, "op:"+c.opPattern)
		}
	}
	return strings.Join(parts, " ")
}

// wrapFooterItems arranges items into lines that fit within the given width.
// Each line starts with "  " and items are separated by "  ".
func wrapFooterItems(items []string, width int) string {
	if width <= 0 {
		return "  " + strings.Join(items, "  ")
	}

	const prefix = "  "
	const sep = "  "

	var lines []string
	line := prefix

	for _, item := range items {
		switch {
		case line == prefix:
			// First item on a new line — always add it.
			line += item
		case len(line)+len(sep)+len(item) <= width:
			line += sep + item
		default:
			// Wrap to next line.
			lines = append(lines, line)
			line = prefix + item
		}
	}
	if line != prefix {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
