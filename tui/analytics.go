package tui

import (
	"context"
	"fmt"
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
)

func (s analyticsSortMode) String() string {
	switch s {
	case analyticsSortTotalDuration:
		return "total"
	case analyticsSortCount:
		return "count"
	case analyticsSortAvgDuration:
		return "avg"
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
		return analyticsSortTotalDuration
	}
	return analyticsSortTotalDuration
}

type analyticsRow struct {
	query         string
	count         int
	totalDuration time.Duration
	avgDuration   time.Duration
}

func (m Model) buildAnalyticsRows() []analyticsRow {
	type agg struct {
		count    int
		totalDur time.Duration
	}
	groups := make(map[string]*agg)

	for _, ev := range m.events {
		switch proxy.Op(ev.GetOp()) {
		case proxy.OpBegin, proxy.OpCommit, proxy.OpRollback, proxy.OpBind, proxy.OpPrepare:
			continue
		case proxy.OpQuery, proxy.OpExec, proxy.OpExecute:
		}

		q := ev.GetQuery()
		if q == "" {
			continue
		}

		g, ok := groups[q]
		if !ok {
			g = &agg{}
			groups[q] = g
		}
		g.count++
		g.totalDur += ev.GetDuration().AsDuration()
	}

	rows := make([]analyticsRow, 0, len(groups))
	for q, g := range groups {
		rows = append(rows, analyticsRow{
			query:         q,
			count:         g.count,
			totalDuration: g.totalDur,
			avgDuration:   g.totalDur / time.Duration(g.count),
		})
	}
	return rows
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
		m.displayRows, m.txColorMap = m.rebuildDisplayRows()
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
	analyticsColTotal  = 10 // "     Total" right-aligned
)

func (m Model) analyticsVisibleRows() int {
	return max(m.height-4, 3) // -2 for top/bottom border, -1 for header, -1 for padding
}

func (m Model) analyticsMaxLineWidth() int {
	maxW := 0
	for _, r := range m.analyticsRows {
		w := analyticsColMarker + analyticsColCount + analyticsColAvg + analyticsColTotal + 4 + len([]rune(r.query))
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

	// 4 = separator spaces: count" "avg" "total"  "query
	colQuery := max(innerWidth-analyticsColMarker-analyticsColCount-analyticsColAvg-analyticsColTotal-4, 10)

	header := fmt.Sprintf("  %*s %*s %*s  %s",
		analyticsColCount, "Count",
		analyticsColAvg, "Avg",
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

		row := fmt.Sprintf("%s%*d %*s %*s  %s",
			marker,
			analyticsColCount, r.count,
			analyticsColAvg, formatDurationValue(r.avgDuration),
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
