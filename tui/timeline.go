package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	tapv1 "github.com/mickamy/sql-tap/gen/tap/v1"
	"github.com/mickamy/sql-tap/proxy"
)

const tlLabelWidth = 40

func (m Model) timelineEvents() []int {
	matched := matchingEventsFiltered(m.events, m.filterQuery, m.searchQuery)
	var indices []int
	for i, ev := range m.events {
		if !matched[i] {
			continue
		}
		switch proxy.Op(ev.GetOp()) {
		case proxy.OpQuery, proxy.OpExec, proxy.OpExecute:
			indices = append(indices, i)
		case proxy.OpPrepare, proxy.OpBind, proxy.OpBegin, proxy.OpCommit, proxy.OpRollback:
		}
	}
	return indices
}

func (m Model) timelineVisibleRows() int {
	return max(m.height-4, 3)
}

func tlBarColor(ev *tapv1.QueryEvent) lipgloss.Color {
	if ev.GetError() != "" {
		return lipgloss.Color("1") // red
	}
	if ev.GetNPlus_1() {
		return lipgloss.Color("3") // yellow
	}
	if ev.GetSlowQuery() {
		return lipgloss.Color("5") // purple
	}
	return lipgloss.Color("4") // blue
}

func (m Model) updateTimeline(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	indices := m.timelineEvents()
	dataRows := max(m.timelineVisibleRows()-2, 1)
	maxScroll := max(len(indices)-dataRows, 0)
	m.timelineScroll = min(m.timelineScroll, maxScroll)

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
		if m.timelineScroll < maxScroll {
			m.timelineScroll++
		}
		return m, nil
	case "k", "up":
		if m.timelineScroll > 0 {
			m.timelineScroll--
		}
		return m, nil
	case "ctrl+d", "pgdown":
		half := max(m.timelineVisibleRows()/2, 1)
		m.timelineScroll = min(m.timelineScroll+half, maxScroll)
		return m, nil
	case "ctrl+u", "pgup":
		half := max(m.timelineVisibleRows()/2, 1)
		m.timelineScroll = max(m.timelineScroll-half, 0)
		return m, nil
	}
	return m, nil
}

func (m Model) renderTimeline() string {
	innerWidth := max(m.width-4, 20)
	visibleRows := m.timelineVisibleRows()

	indices := m.timelineEvents()
	title := fmt.Sprintf(" Timeline (%d queries) ", len(indices))
	borderColor := lipgloss.Color("240")

	if len(indices) == 0 {
		content := "No query events to display"
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Width(innerWidth).
			BorderForeground(borderColor).
			Render(content)
		return box
	}

	// Compute time range.
	minT := m.events[indices[0]].GetStartTime().AsTime()
	maxT := minT
	for _, idx := range indices {
		ev := m.events[idx]
		t0 := ev.GetStartTime().AsTime()
		t1 := t0.Add(ev.GetDuration().AsDuration())
		if t0.Before(minT) {
			minT = t0
		}
		if t1.After(maxT) {
			maxT = t1
		}
	}
	spanMs := float64(maxT.Sub(minT).Milliseconds())
	if spanMs < 1 {
		spanMs = 1
	}

	// Label and chart widths.
	labelW := min(tlLabelWidth, innerWidth/2)
	chartWidth := max(innerWidth-labelW-1, 10) // -1 for │ separator

	// Scroll.
	dataRows := max(visibleRows-2, 1) // -1 for axis header, -1 for padding
	maxScroll := max(len(indices)-dataRows, 0)
	scroll := min(m.timelineScroll, maxScroll)
	start := scroll
	end := min(start+dataRows, len(indices))

	// Build rows.
	var rows []string
	rows = append(rows, renderTimeAxis(spanMs, chartWidth, labelW))

	faintSep := lipgloss.NewStyle().Faint(true).Render("│")
	for i := start; i < end; i++ {
		idx := indices[i]
		ev := m.events[idx]

		q := ev.GetQuery()
		if q == "" {
			q = proxy.Op(ev.GetOp()).String()
		}
		q = truncate(q, labelW-1)
		label := fmt.Sprintf("%-*s", labelW, q)

		t0 := ev.GetStartTime().AsTime()
		dur := ev.GetDuration().AsDuration()
		startFrac := float64(t0.Sub(minT).Milliseconds()) / spanMs
		durFrac := float64(dur.Milliseconds()) / spanMs

		barStart := int(startFrac * float64(chartWidth))
		barWidth := max(int(math.Ceil(durFrac*float64(chartWidth))), 1)
		barEnd := min(barStart+barWidth, chartWidth)

		pre := strings.Repeat(" ", barStart)
		bar := strings.Repeat("█", barEnd-barStart)
		post := strings.Repeat(" ", max(chartWidth-barEnd, 0))

		coloredBar := lipgloss.NewStyle().Foreground(tlBarColor(ev)).Render(bar)
		rows = append(rows, label+faintSep+pre+coloredBar+post)
	}

	content := strings.Join(rows, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(innerWidth).
		BorderForeground(borderColor).
		Render(content)

	// Custom title.
	boxLines := strings.Split(box, "\n")
	if len(boxLines) > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		titleStyle := lipgloss.NewStyle().Bold(true)
		dashes := max(innerWidth-len([]rune(title)), 0)
		boxLines[0] = borderFg.Render("╭") +
			titleStyle.Render(title) +
			borderFg.Render(strings.Repeat("─", dashes)+"╮")
	}

	// Help footer.
	if n := len(boxLines); n > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		help := " q: back  j/k: scroll  ctrl+d/u: page "
		dashes := max(innerWidth-len([]rune(help)), 0)
		boxLines[n-1] = borderFg.Render("╰") +
			lipgloss.NewStyle().Faint(true).Render(help) +
			borderFg.Render(strings.Repeat("─", dashes)+"╯")
	}

	return strings.Join(boxLines, "\n")
}

func renderTimeAxis(spanMs float64, chartWidth, labelWidth int) string {
	tickCount := max(min(chartWidth/12, 6), 2)

	line := make([]rune, chartWidth)
	for i := range line {
		line[i] = ' '
	}

	for i := 0; i <= tickCount; i++ {
		frac := float64(i) / float64(tickCount)
		ms := frac * spanMs
		dur := time.Duration(ms * float64(time.Millisecond))
		label := formatDurationValue(dur)

		col := int(frac * float64(chartWidth-1))
		for j, r := range []rune(label) {
			pos := col + j
			if pos < chartWidth {
				line[pos] = r
			}
		}
	}

	labelPad := strings.Repeat(" ", labelWidth)
	faint := lipgloss.NewStyle().Faint(true)
	return faint.Render(labelPad + "│" + string(line))
}
