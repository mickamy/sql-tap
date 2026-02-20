package tui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mickamy/sql-tap/clipboard"
	"github.com/mickamy/sql-tap/proxy"
)

type analyticsSortMode int

const (
	analyticsSortTotalDuration analyticsSortMode = iota
	analyticsSortCount
	analyticsSortAvgDuration
	analyticsSortP95Duration
)

func (s analyticsSortMode) String() string {
	switch s {
	case analyticsSortTotalDuration:
		return "total"
	case analyticsSortCount:
		return "count"
	case analyticsSortAvgDuration:
		return "avg"
	case analyticsSortP95Duration:
		return "p95"
	}
	return "total"
}

func (s analyticsSortMode) next() analyticsSortMode {
	switch s {
	case analyticsSortTotalDuration:
		return analyticsSortCount
	case analyticsSortCount:
		return analyticsSortAvgDuration
	case analyticsSortAvgDuration:
		return analyticsSortP95Duration
	case analyticsSortP95Duration:
		return analyticsSortTotalDuration
	}
	return analyticsSortTotalDuration
}

type analyticsRow struct {
	query         string
	count         int
	totalDuration time.Duration
	avgDuration   time.Duration
	p95Duration   time.Duration
	maxDuration   time.Duration
}

func (m Model) buildAnalyticsRows() []analyticsRow {
	type agg struct {
		count     int
		totalDur  time.Duration
		durations []time.Duration
	}
	groups := make(map[string]*agg)

	for _, ev := range m.events {
		switch proxy.Op(ev.GetOp()) {
		case proxy.OpBegin, proxy.OpCommit, proxy.OpRollback, proxy.OpBind, proxy.OpPrepare:
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
		}
		g.count++
		g.totalDur += dur
		g.durations = append(g.durations, dur)
	}

	rows := make([]analyticsRow, 0, len(groups))
	for q, g := range groups {
		slices.SortFunc(g.durations, cmp.Compare)
		rows = append(rows, analyticsRow{
			query:         q,
			count:         g.count,
			totalDuration: g.totalDur,
			avgDuration:   g.totalDur / time.Duration(g.count),
			p95Duration:   percentile(g.durations, 0.95),
			maxDuration:   g.durations[len(g.durations)-1],
		})
	}
	return rows
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func sortAnalyticsRows(rows []analyticsRow, mode analyticsSortMode) {
	sort.Slice(rows, func(i, j int) bool {
		switch mode {
		case analyticsSortTotalDuration:
			return rows[i].totalDuration > rows[j].totalDuration
		case analyticsSortCount:
			return rows[i].count > rows[j].count
		case analyticsSortAvgDuration:
			return rows[i].avgDuration > rows[j].avgDuration
		case analyticsSortP95Duration:
			return rows[i].p95Duration > rows[j].p95Duration
		}
		return rows[i].totalDuration > rows[j].totalDuration
	})
}

func (m Model) updateAnalytics(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.conn != nil {
			_ = m.conn.Close()
		}
		return m, tea.Quit
	case "q":
		m.view = viewList
		m = m.rebuild()
		if m.follow {
			m.cursor = max(len(m.displayRows)-1, 0)
		}
		return m, nil
	case "j", "down":
		if len(m.analyticsRows) > 0 && m.analyticsCursor < len(m.analyticsRows)-1 {
			m.analyticsCursor++
		}
		return m, nil
	case "k", "up":
		if m.analyticsCursor > 0 {
			m.analyticsCursor--
		}
		return m, nil
	case "h", "left":
		if m.analyticsHScroll > 0 {
			m.analyticsHScroll--
		}
		return m, nil
	case "l", "right":
		innerWidth := max(m.width-4, 20)
		maxW := m.analyticsMaxLineWidth()
		maxHScroll := max(maxW-innerWidth, 0)
		if m.analyticsHScroll < maxHScroll {
			m.analyticsHScroll++
		}
		return m, nil
	case "ctrl+d":
		half := m.analyticsVisibleRows() / 2
		m.analyticsCursor = min(m.analyticsCursor+half, max(len(m.analyticsRows)-1, 0))
		return m, nil
	case "ctrl+u":
		half := m.analyticsVisibleRows() / 2
		m.analyticsCursor = max(m.analyticsCursor-half, 0)
		return m, nil
	case "s":
		m.analyticsSortMode = m.analyticsSortMode.next()
		sortAnalyticsRows(m.analyticsRows, m.analyticsSortMode)
		m.analyticsCursor = 0
		return m, nil
	case "c":
		if m.analyticsCursor >= 0 && m.analyticsCursor < len(m.analyticsRows) {
			_ = clipboard.Copy(context.Background(), m.analyticsRows[m.analyticsCursor].query)
			return m.showAlert("copied!")
		}
		return m, nil
	}
	return m, nil
}

const (
	analyticsColMarker = 2  // "▶ " or "  "
	analyticsColCount  = 7  // "  Count" right-aligned
	analyticsColAvg    = 10 // "       Avg" right-aligned
	analyticsColP95    = 10 // "       P95" right-aligned
	analyticsColMax    = 10 // "       Max" right-aligned
	analyticsColTotal  = 10 // "     Total" right-aligned
)

func (m Model) analyticsVisibleRows() int {
	return max(m.height-4, 3) // -2 for top/bottom border, -1 for header, -1 for padding
}

func (m Model) analyticsMaxLineWidth() int {
	fixedCols := analyticsColMarker + analyticsColCount + analyticsColAvg +
		analyticsColP95 + analyticsColMax + analyticsColTotal + 6
	maxW := 0
	for _, r := range m.analyticsRows {
		w := fixedCols + len([]rune(r.query))
		if w > maxW {
			maxW = w
		}
	}
	return maxW
}

func (m Model) renderAnalytics() string {
	innerWidth := max(m.width-4, 20)
	visibleRows := m.analyticsVisibleRows()

	title := fmt.Sprintf(" Analytics (%d templates) [sort: %s] ", len(m.analyticsRows), m.analyticsSortMode)

	// 6 = separator spaces between columns
	fixedWidth := analyticsColMarker + analyticsColCount + analyticsColAvg +
		analyticsColP95 + analyticsColMax + analyticsColTotal + 6
	colQuery := max(innerWidth-fixedWidth, 10)

	header := fmt.Sprintf("  %*s %*s %*s %*s %*s  %s",
		analyticsColCount, "Count",
		analyticsColAvg, "Avg",
		analyticsColP95, "P95",
		analyticsColMax, "Max",
		analyticsColTotal, "Total",
		"Query",
	)

	dataRows := max(visibleRows-1, 1) // -1 for header

	start := 0
	if len(m.analyticsRows) > dataRows {
		start = max(m.analyticsCursor-dataRows/2, 0)
		if start+dataRows > len(m.analyticsRows) {
			start = len(m.analyticsRows) - dataRows
		}
	}
	end := min(start+dataRows, len(m.analyticsRows))

	var rows []string
	rows = append(rows, lipgloss.NewStyle().Bold(true).Render(header))
	for i := start; i < end; i++ {
		r := m.analyticsRows[i]
		marker := "  "
		if i == m.analyticsCursor {
			marker = "▶ "
		}

		q := strings.TrimSpace(reSpaces.ReplaceAllString(r.query, " "))
		// Apply horizontal scroll then truncate.
		runes := []rune(q)
		if m.analyticsHScroll < len(runes) {
			runes = runes[m.analyticsHScroll:]
		} else {
			runes = nil
		}
		q = string(runes)
		if len([]rune(q)) > colQuery {
			q = string([]rune(q)[:colQuery-1]) + "…"
		}

		row := fmt.Sprintf("%s%*d %*s %*s %*s %*s  %s",
			marker,
			analyticsColCount, r.count,
			analyticsColAvg, formatDurationValue(r.avgDuration),
			analyticsColP95, formatDurationValue(r.p95Duration),
			analyticsColMax, formatDurationValue(r.maxDuration),
			analyticsColTotal, formatDurationValue(r.totalDuration),
			q,
		)
		rows = append(rows, row)
	}

	content := strings.Join(rows, "\n")

	borderColor := lipgloss.Color("240")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(innerWidth).
		BorderForeground(borderColor).
		Render(content)

	boxLines := strings.Split(box, "\n")
	if len(boxLines) > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		titleStyle := lipgloss.NewStyle().Bold(true)
		dashes := max(innerWidth-len([]rune(title)), 0)
		boxLines[0] = borderFg.Render("╭") +
			titleStyle.Render(title) +
			borderFg.Render(strings.Repeat("─", dashes)+"╮")
	}

	if n := len(boxLines); n > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		help := " q: back  j/k: scroll  h/l: pan  s: sort  c: copy "
		dashes := max(innerWidth-len([]rune(help)), 0)
		boxLines[n-1] = borderFg.Render("╰") +
			lipgloss.NewStyle().Faint(true).Render(help) +
			borderFg.Render(strings.Repeat("─", dashes)+"╯")
	}

	return strings.Join(boxLines, "\n")
}
