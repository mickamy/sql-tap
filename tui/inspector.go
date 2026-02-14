package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mickamy/sql-tap/clipboard"
	"github.com/mickamy/sql-tap/explain"
	"github.com/mickamy/sql-tap/highlight"
	"github.com/mickamy/sql-tap/query"
)

func (m Model) updateInspect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.conn != nil {
			_ = m.conn.Close()
		}
		return m, tea.Quit
	case "q":
		m.view = viewList
		m.displayRows, m.txColorMap = rebuildDisplayRows(m.events, m.collapsed, m.searchQuery)
		if m.follow {
			m.cursor = max(len(m.displayRows)-1, 0)
		}
		return m, nil
	case "x":
		return m.startExplain(explain.Explain)
	case "X":
		return m.startExplain(explain.Analyze)
	case "c":
		ev := m.cursorEvent()
		if ev == nil || ev.GetQuery() == "" {
			return m, nil
		}
		_ = clipboard.Copy(context.Background(), ev.GetQuery())
		return m, nil
	case "C":
		ev := m.cursorEvent()
		if ev == nil || ev.GetQuery() == "" {
			return m, nil
		}
		_ = clipboard.Copy(context.Background(), query.Bind(ev.GetQuery(), ev.GetArgs()))
		return m, nil
	case "e":
		return m.startEditExplain(explain.Explain)
	case "E":
		return m.startEditExplain(explain.Analyze)
	case "j", "down":
		maxScroll := max(len(m.inspectLines())-m.inspectVisibleRows(), 0)
		if m.inspectScroll < maxScroll {
			m.inspectScroll++
		}
		return m, nil
	case "k", "up":
		if m.inspectScroll > 0 {
			m.inspectScroll--
		}
		return m, nil
	}
	return m, nil
}

func (m Model) inspectLines() []string {
	if m.cursor < 0 || m.cursor >= len(m.displayRows) {
		return nil
	}
	dr := m.displayRows[m.cursor]
	innerWidth := max(m.width-4, 20)
	switch dr.kind {
	case rowTxSummary:
		return m.inspectorTxLines(dr, innerWidth)
	case rowEvent:
		return m.inspectorEventLines(dr)
	}
	return nil
}

func (m Model) inspectVisibleRows() int {
	return max(m.height-2, 3) // -2 for top/bottom border
}

func (m Model) renderInspector() string {
	innerWidth := max(m.width-4, 20)
	visibleRows := m.inspectVisibleRows()

	lines := m.inspectLines()
	if lines == nil {
		return ""
	}

	// clamp scroll
	maxScroll := max(len(lines)-visibleRows, 0)
	if m.inspectScroll > maxScroll {
		m.inspectScroll = maxScroll
	}

	end := min(m.inspectScroll+visibleRows, len(lines))
	visible := lines[m.inspectScroll:end]
	content := strings.Join(visible, "\n")

	borderColor := lipgloss.Color("240")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(innerWidth).
		BorderForeground(borderColor).
		Render(content)

	// Replace top border with title
	boxLines := strings.Split(box, "\n")
	if len(boxLines) > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		titleStyle := lipgloss.NewStyle().Bold(true)
		title := " Inspector "
		dashes := max(innerWidth-len([]rune(title)), 0)
		boxLines[0] = borderFg.Render("╭") +
			titleStyle.Render(title) +
			borderFg.Render(strings.Repeat("─", dashes)+"╮")
	}

	// Replace bottom border with help
	if n := len(boxLines); n > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		help := " q: back  j/k: scroll  c: copy query  C: copy with args  x/X: explain/analyze  e/E: edit+explain "
		dashes := max(innerWidth-len([]rune(help)), 0)
		boxLines[n-1] = borderFg.Render("╰") +
			lipgloss.NewStyle().Faint(true).Render(help) +
			borderFg.Render(strings.Repeat("─", dashes)+"╯")
	}

	return strings.Join(boxLines, "\n")
}

func (m Model) inspectorTxLines(dr displayRow, innerWidth int) []string {
	nq := m.txQueryCount(dr.events)
	dur := m.txWallDuration(dr.events)

	lines := make([]string, 0, 7+len(dr.events))
	lines = append(lines, "Type:     Transaction")

	label := fmt.Sprintf("%d queries", nq)
	if nq == 1 {
		label = "1 query"
	}
	lines = append(lines, "Queries:  "+label)
	lines = append(lines, "Duration: "+formatDurationValue(dur))
	lines = append(lines, "Time:     "+formatTimeFull(m.events[dr.events[0]].GetStartTime()))
	lines = append(lines, "Tx:       "+dr.txID)

	lines = append(lines, "")
	lines = append(lines, "Events:")
	for _, idx := range dr.events {
		ev := m.events[idx]
		op := opString(ev.GetOp())
		q := truncate(ev.GetQuery(), max(innerWidth-24, 20))
		if q == "" {
			q = "-"
		}
		q = highlight.SQL(q)
		dur := formatDuration(ev.GetDuration())
		lines = append(lines, fmt.Sprintf("  %-8s %s %s", op, q, dur))
	}

	return lines
}

func (m Model) inspectorEventLines(dr displayRow) []string {
	ev := m.events[dr.eventIdx]

	var lines []string
	lines = append(lines, "Op:       "+opString(ev.GetOp()))

	if q := ev.GetQuery(); q != "" {
		lines = append(lines, "Query:")
		for l := range strings.SplitSeq(q, "\n") {
			lines = append(lines, "  "+highlight.SQL(strings.TrimSpace(l)))
		}
	}

	if len(ev.GetArgs()) > 0 {
		lines = append(lines,
			fmt.Sprintf("Args:     [%s]", strings.Join(ev.GetArgs(), ", ")))
	}

	lines = append(lines, "Duration: "+formatDuration(ev.GetDuration()))
	lines = append(lines, "Time:     "+formatTimeFull(ev.GetStartTime()))

	if ev.GetRowsAffected() > 0 {
		lines = append(lines, fmt.Sprintf("Rows:     %d", ev.GetRowsAffected()))
	}

	if ev.GetError() != "" {
		lines = append(lines, "Error:    "+ev.GetError())
	}

	if ev.GetTxId() != "" {
		lines = append(lines, "Tx:       "+ev.GetTxId())
	}

	return lines
}
