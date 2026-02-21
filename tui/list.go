package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	tapv1 "github.com/mickamy/sql-tap/gen/tap/v1"
	"github.com/mickamy/sql-tap/highlight"
	"github.com/mickamy/sql-tap/proxy"
)

func eventStatus(ev *tapv1.QueryEvent) string {
	if ev.GetError() != "" {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).Render("E")
	}
	if ev.GetNPlus_1() {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")).Render("N+1")
	}
	if ev.GetSlowQuery() {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).Render("SLOW")
	}
	return ""
}

// Column widths.
const (
	colMarker   = 4 // "▶ " or "▾ " (2) + indent/space (2)
	colOp       = 9
	colDuration = 10
	colTime     = 12
	colStatus   = 4
)

// txColors is a palette for coloring transaction rows.
var txColors = []lipgloss.Color{"6", "3", "5", "2", "4", "1"}

func (m Model) renderList(maxRows int) string {
	innerWidth := max(m.width-4, 20)
	colQuery := max(innerWidth-colMarker-colOp-colDuration-colTime-colStatus-4, 10)

	var title string
	if m.searchQuery != "" || m.filterQuery != "" {
		matched := 0
		for _, dr := range m.displayRows {
			if dr.kind == rowEvent {
				matched++
			}
		}
		title = fmt.Sprintf(" sql-tap (%d/%d queries) ", matched, len(m.events))
	} else {
		title = fmt.Sprintf(" sql-tap (%d queries) ", len(m.events))
	}
	if m.sortMode == sortDuration {
		title += "[slow] "
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(innerWidth)

	dataRows := max(maxRows-1, 1) // -1 for header row

	start := 0
	if len(m.displayRows) > dataRows {
		start = max(m.cursor-dataRows/2, 0)
		if start+dataRows > len(m.displayRows) {
			start = len(m.displayRows) - dataRows
		}
	}
	end := min(start+dataRows, len(m.displayRows))

	header := fmt.Sprintf("    %-*s %-*s %*s %*s %-*s",
		colOp, "Op",
		colQuery, "Query",
		colDuration, "Duration",
		colTime, "Time",
		colStatus, "",
	)

	var rows []string
	rows = append(rows, lipgloss.NewStyle().Bold(true).Render(header))
	for i := start; i < end; i++ {
		dr := m.displayRows[i]
		isCursor := i == m.cursor

		switch dr.kind {
		case rowTxSummary:
			rows = append(rows, m.renderTxSummaryRow(dr, isCursor, colQuery))
		case rowEvent:
			rows = append(rows, m.renderEventRow(dr, i, isCursor, colQuery))
		}
	}

	borderColor := lipgloss.Color("240")
	border = border.BorderForeground(borderColor)
	content := strings.Join(rows, "\n")

	box := border.Render(content)
	lines := strings.Split(box, "\n")
	if len(lines) > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		titleStyle := lipgloss.NewStyle().Bold(true)
		dashes := max(innerWidth-len([]rune(title)), 0)
		lines[0] = borderFg.Render("╭") +
			titleStyle.Render(title) +
			borderFg.Render(strings.Repeat("─", dashes)+"╮")
		box = strings.Join(lines, "\n")
	}

	return box
}

func (m Model) renderTxSummaryRow(dr displayRow, isCursor bool, colQuery int) string {
	marker := "  "
	if isCursor {
		marker = "▶ "
	}

	chevron := "▾ "
	if m.collapsed[dr.txID] {
		chevron = "▸ "
	}

	nq := m.txQueryCount(dr.events)
	label := fmt.Sprintf("%d queries", nq)
	if nq == 1 {
		label = "1 query"
	}

	dur := formatDurationValue(m.txWallDuration(dr.events))
	t := formatTime(m.events[dr.events[0]].GetStartTime())

	styled := lipgloss.NewStyle().Foreground(m.txColorMap[dr.txID])

	if isCursor {
		styled = styled.Bold(true)
		bold := lipgloss.NewStyle().Bold(true)
		return bold.Render(marker) +
			styled.Render(chevron) +
			padRight(styled.Render("Tx"), colOp) + " " +
			padRight(bold.Render(label), colQuery) + " " +
			padLeft(bold.Render(dur), colDuration) + " " +
			padLeft(bold.Render(t), colTime)
	}

	return fmt.Sprintf("%s%s%s %-*s %*s %*s",
		marker,
		styled.Render(chevron),
		padRight(styled.Render("Tx"), colOp),
		colQuery, label,
		colDuration, dur,
		colTime, t,
	)
}

func (m Model) renderEventRow(dr displayRow, drIdx int, isCursor bool, colQuery int) string {
	ev := m.events[dr.eventIdx]
	marker := "  "
	if isCursor {
		marker = "▶ "
	}

	op := opString(ev.GetOp())
	dur := formatDuration(ev.GetDuration())
	t := formatTime(ev.GetStartTime())

	indent := "  " // non-tx: align with chevron space
	cq := colQuery
	if m.isTxChild(drIdx) {
		indent = "    " // tx child: extra indent
		cq = max(colQuery-2, 1)
	}

	q := truncate(ev.GetQuery(), cq)
	if q == "" {
		q = "-"
	}

	status := eventStatus(ev)

	if m.isTxChild(drIdx) {
		styled := lipgloss.NewStyle().Foreground(m.txColorMap[ev.GetTxId()])
		if isCursor {
			styled = styled.Bold(true)
			bold := lipgloss.NewStyle().Bold(true)
			return bold.Render(marker) +
				bold.Render(indent) +
				padRight(styled.Render(op), colOp) + " " +
				padRight(bold.Render(q), cq) + " " +
				padLeft(bold.Render(dur), colDuration) + " " +
				padLeft(bold.Render(t), colTime) + " " +
				status
		}
		return fmt.Sprintf("%s%s%s %-*s %*s %*s",
			marker,
			indent,
			padRight(styled.Render(op), colOp),
			cq, q,
			colDuration, dur,
			colTime, t,
		) + " " + status
	}

	row := fmt.Sprintf("%s%s%-*s %-*s %*s %*s",
		marker,
		indent,
		colOp, op,
		cq, q,
		colDuration, dur,
		colTime, t,
	) + " " + status
	if isCursor {
		row = lipgloss.NewStyle().Bold(true).Render(row)
	}
	return row
}

func (m Model) renderPreview() string {
	innerWidth := max(m.width-4, 20)

	if m.cursor < 0 || m.cursor >= len(m.displayRows) {
		return ""
	}

	dr := m.displayRows[m.cursor]

	switch dr.kind {
	case rowTxSummary:
		return m.renderTxPreview(dr, innerWidth)
	case rowEvent:
		return m.renderEventPreview(dr, innerWidth)
	}

	return ""
}

func (m Model) renderTxPreview(dr displayRow, innerWidth int) string {
	nq := m.txQueryCount(dr.events)
	dur := m.txWallDuration(dr.events)

	var lines []string
	lines = append(lines, "Type:     Transaction")

	label := fmt.Sprintf("%d queries", nq)
	if nq == 1 {
		label = "1 query"
	}
	lines = append(lines, "Queries:  "+label)
	lines = append(lines, "Duration: "+formatDurationValue(dur))
	lines = append(lines, "Tx:       "+dr.txID)

	maxQueryLen := max(innerWidth-14, 20) // 14 = len("  Query   ") + padding
	for _, idx := range dr.events {
		ev := m.events[idx]
		op := proxy.Op(ev.GetOp())
		switch op {
		case proxy.OpBegin, proxy.OpCommit, proxy.OpRollback, proxy.OpBind, proxy.OpPrepare:
		case proxy.OpQuery, proxy.OpExec, proxy.OpExecute:
			q := truncate(ev.GetQuery(), maxQueryLen)
			lines = append(lines, fmt.Sprintf("  %-8s %s", op.String(), highlight.SQL(q)))
		}
	}

	content := strings.Join(lines, "\n")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(innerWidth).
		BorderForeground(lipgloss.Color("240"))

	return border.Render(content)
}

func (m Model) renderEventPreview(dr displayRow, innerWidth int) string {
	ev := m.events[dr.eventIdx]

	var lines []string
	lines = append(lines, "Op:       "+opString(ev.GetOp()))

	if q := ev.GetQuery(); q != "" {
		maxQueryLen := max(innerWidth-10, 20) // 10 = len("Query:    ")
		lines = append(lines, "Query:    "+highlight.SQL(truncate(q, maxQueryLen)))
	}

	if len(ev.GetArgs()) > 0 {
		lines = append(lines, fmt.Sprintf("Args:     [%s]", strings.Join(ev.GetArgs(), ", ")))
	}

	lines = append(lines, "Duration: "+formatDuration(ev.GetDuration()))

	if ev.GetError() != "" {
		lines = append(lines, "Error:    "+ev.GetError())
	}

	if ev.GetTxId() != "" {
		lines = append(lines, "Tx:       "+ev.GetTxId())
	}

	content := strings.Join(lines, "\n")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(innerWidth).
		BorderForeground(lipgloss.Color("240"))

	return border.Render(content)
}
