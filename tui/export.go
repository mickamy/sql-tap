package tui

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tapv1 "github.com/mickamy/sql-tap/gen/tap/v1"
	"github.com/mickamy/sql-tap/proxy"
)

type exportFormat int

const (
	exportJSON exportFormat = iota
	exportMarkdown
)

func (f exportFormat) ext() string {
	if f == exportMarkdown {
		return "md"
	}
	return "json"
}

type exportAnalyticsRow struct {
	Query   string  `json:"query"`
	Count   int     `json:"count"`
	TotalMs float64 `json:"total_ms"`
	AvgMs   float64 `json:"avg_ms"`
	P95Ms   float64 `json:"p95_ms"`
	MaxMs   float64 `json:"max_ms"`
}

type exportQuery struct {
	Time         string   `json:"time"`
	Op           string   `json:"op"`
	Query        string   `json:"query"`
	Args         []string `json:"args"`
	DurationMs   float64  `json:"duration_ms"`
	RowsAffected int64    `json:"rows_affected"`
	Error        string   `json:"error"`
	TxID         string   `json:"tx_id"`
}

type exportData struct {
	Captured int    `json:"captured"`
	Exported int    `json:"exported"`
	Filter   string `json:"filter"`
	Search   string `json:"search"`
	Period   struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"period"`
	Queries   []exportQuery        `json:"queries"`
	Analytics []exportAnalyticsRow `json:"analytics"`
}

// filteredEvents returns the subset of events matching filter and search.
func filteredEvents(
	events []*tapv1.QueryEvent, filterQuery, searchQuery string,
) []*tapv1.QueryEvent {
	matched := matchingEventsFiltered(events, filterQuery, searchQuery)
	result := make([]*tapv1.QueryEvent, 0, len(matched))
	for i, ev := range events {
		if matched[i] {
			result = append(result, ev)
		}
	}
	return result
}

// buildExportAnalytics aggregates query metrics from the given events.
func buildExportAnalytics(events []*tapv1.QueryEvent) []exportAnalyticsRow {
	type agg struct {
		count     int
		totalDur  time.Duration
		durations []time.Duration
	}
	groups := make(map[string]*agg)
	var order []string

	for _, ev := range events {
		switch proxy.Op(ev.GetOp()) {
		case proxy.OpBegin, proxy.OpCommit, proxy.OpRollback,
			proxy.OpBind, proxy.OpPrepare:
			continue
		case proxy.OpQuery, proxy.OpExec, proxy.OpExecute:
		}
		nq := ev.GetNormalizedQuery()
		if nq == "" {
			continue
		}
		dur := ev.GetDuration().AsDuration()
		g, ok := groups[nq]
		if !ok {
			g = &agg{}
			groups[nq] = g
			order = append(order, nq)
		}
		g.count++
		g.totalDur += dur
		g.durations = append(g.durations, dur)
	}

	rows := make([]exportAnalyticsRow, 0, len(groups))
	for _, q := range order {
		g := groups[q]
		slices.SortFunc(g.durations, cmp.Compare)
		totalMs := float64(g.totalDur.Microseconds()) / 1000
		avgMs := totalMs / float64(g.count)
		p95Ms := float64(percentile(g.durations, 0.95).Microseconds()) / 1000
		maxMs := float64(g.durations[len(g.durations)-1].Microseconds()) / 1000
		rows = append(rows, exportAnalyticsRow{
			Query:   q,
			Count:   g.count,
			TotalMs: totalMs,
			AvgMs:   avgMs,
			P95Ms:   p95Ms,
			MaxMs:   maxMs,
		})
	}
	return rows
}

func buildExportData(
	allEvents []*tapv1.QueryEvent, filterQuery, searchQuery string,
) exportData {
	exported := filteredEvents(allEvents, filterQuery, searchQuery)

	var d exportData
	d.Captured = len(allEvents)
	d.Exported = len(exported)
	d.Filter = filterQuery
	d.Search = searchQuery

	if len(exported) > 0 {
		first := exported[0].GetStartTime()
		last := exported[len(exported)-1].GetStartTime()
		//nolint:gosmopolitan // export uses local time
		d.Period.Start = first.AsTime().In(time.Local).Format("15:04:05")
		//nolint:gosmopolitan // export uses local time
		d.Period.End = last.AsTime().In(time.Local).Format("15:04:05")
	}

	d.Queries = make([]exportQuery, 0, len(exported))
	for _, ev := range exported {
		args := ev.GetArgs()
		if args == nil {
			args = []string{}
		}
		var durMs float64
		if dur := ev.GetDuration(); dur != nil {
			durMs = float64(dur.AsDuration().Microseconds()) / 1000
		}
		//nolint:gosmopolitan // export uses local time
		ts := ev.GetStartTime().AsTime().In(time.Local)
		d.Queries = append(d.Queries, exportQuery{
			Time:         ts.Format("15:04:05.000"),
			Op:           opString(ev.GetOp()),
			Query:        ev.GetQuery(),
			Args:         args,
			DurationMs:   durMs,
			RowsAffected: ev.GetRowsAffected(),
			Error:        ev.GetError(),
			TxID:         ev.GetTxId(),
		})
	}

	d.Analytics = buildExportAnalytics(exported)
	return d
}

func renderJSON(
	allEvents []*tapv1.QueryEvent, filterQuery, searchQuery string,
) (string, error) {
	d := buildExportData(allEvents, filterQuery, searchQuery)
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal export: %w", err)
	}
	return string(b) + "\n", nil
}

func renderMarkdown(
	allEvents []*tapv1.QueryEvent, filterQuery, searchQuery string,
) string {
	d := buildExportData(allEvents, filterQuery, searchQuery)

	var sb strings.Builder
	sb.WriteString("# sql-tap export\n\n")

	fmt.Fprintf(&sb, "- Captured: %d queries\n", d.Captured)
	exportLine := fmt.Sprintf("- Exported: %d queries", d.Exported)
	if d.Filter != "" || d.Search != "" {
		var parts []string
		if d.Filter != "" {
			parts = append(parts, "filter: "+d.Filter)
		}
		if d.Search != "" {
			parts = append(parts, "search: "+d.Search)
		}
		exportLine += " (" + strings.Join(parts, ", ") + ")"
	}
	sb.WriteString(exportLine + "\n")
	if d.Period.Start != "" {
		fmt.Fprintf(&sb, "- Period: %s — %s\n",
			d.Period.Start, d.Period.End)
	}

	sb.WriteString("\n## Queries\n\n")
	sb.WriteString("| # | Time | Op | Duration | Query | Args | Error |\n")
	sb.WriteString("|---|------|----|----------|-------|------|-------|\n")
	for i, q := range d.Queries {
		argsStr := formatArgsForMarkdown(q.Args)
		fmt.Fprintf(&sb, "| %d | %s | %s | %s | %s | %s | %s |\n",
			i+1, q.Time, q.Op,
			formatDurationMs(q.DurationMs),
			escapeMarkdownPipe(q.Query),
			argsStr,
			escapeMarkdownPipe(q.Error),
		)
	}

	if len(d.Analytics) > 0 {
		sb.WriteString("\n## Analytics\n\n")
		sb.WriteString("| Query | Count | Avg | P95 | Max | Total |\n")
		sb.WriteString("|-------|-------|-----|-----|-----|-------|\n")
		for _, a := range d.Analytics {
			fmt.Fprintf(&sb, "| %s | %d | %s | %s | %s | %s |\n",
				escapeMarkdownPipe(a.Query),
				a.Count,
				formatDurationMs(a.AvgMs),
				formatDurationMs(a.P95Ms),
				formatDurationMs(a.MaxMs),
				formatDurationMs(a.TotalMs),
			)
		}
	}

	return sb.String()
}

func formatDurationMs(ms float64) string {
	switch {
	case ms < 1:
		return fmt.Sprintf("%.0fµs", ms*1000)
	case ms < 1000:
		return fmt.Sprintf("%.1fms", ms)
	default:
		return fmt.Sprintf("%.2fs", ms/1000)
	}
}

func formatArgsForMarkdown(args []string) string {
	if len(args) == 0 {
		return ""
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + a + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func escapeMarkdownPipe(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

// writeExport writes filtered events to a file and returns the path.
// dir specifies the output directory; if empty, the current directory is used.
func writeExport(
	allEvents []*tapv1.QueryEvent,
	filterQuery, searchQuery string,
	format exportFormat,
	dir string,
) (string, error) {
	var content string
	var err error

	switch format {
	case exportJSON:
		content, err = renderJSON(allEvents, filterQuery, searchQuery)
		if err != nil {
			return "", err
		}
	case exportMarkdown:
		content = renderMarkdown(allEvents, filterQuery, searchQuery)
	}

	filename := fmt.Sprintf("sql-tap-%s.%s",
		time.Now().Format("20060102-150405"), format.ext())
	if dir != "" {
		filename = filepath.Join(dir, filename)
	}

	if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write export: %w", err)
	}
	return filename, nil
}
