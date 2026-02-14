package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mickamy/sql-tap/clipboard"
	"github.com/mickamy/sql-tap/explain"
	tapv1 "github.com/mickamy/sql-tap/gen/tap/v1"
	"github.com/mickamy/sql-tap/highlight"
)

func (m Model) updateExplain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "j", "down":
		lines := m.explainLines()
		maxScroll := max(len(lines)-m.explainVisibleRows(), 0)
		if m.explainScroll < maxScroll {
			m.explainScroll++
		}
		return m, nil
	case "k", "up":
		if m.explainScroll > 0 {
			m.explainScroll--
		}
		return m, nil
	case "h", "left":
		if m.explainHScroll > 0 {
			m.explainHScroll--
		}
		return m, nil
	case "l", "right":
		innerWidth := max(m.width-4, 20)
		maxW := m.explainMaxLineWidth()
		maxHScroll := max(maxW-innerWidth, 0)
		if m.explainHScroll < maxHScroll {
			m.explainHScroll++
		}
		return m, nil
	case "c":
		if m.explainPlan == "" {
			return m, nil
		}
		_ = clipboard.Copy(context.Background(), m.explainPlan)
		return m, nil
	case "e", "E":
		if m.explainQuery == "" {
			return m, nil
		}
		mode := explain.Explain
		if msg.String() == "E" {
			mode = explain.Analyze
		}
		return m, openEditor(m.explainQuery, m.explainArgs, mode)
	}
	return m, nil
}

func (m Model) explainLines() []string {
	if m.explainErr != nil {
		return []string{"Error: " + m.explainErr.Error()}
	}
	if m.explainPlan == "" {
		return []string{"Running " + m.explainMode.String() + "..."}
	}
	return strings.Split(m.explainPlan, "\n")
}

func (m Model) explainMaxLineWidth() int {
	maxW := 0
	for _, line := range m.explainLines() {
		if w := len([]rune(line)); w > maxW {
			maxW = w
		}
	}
	return maxW
}

func (m Model) explainVisibleRows() int {
	return max(m.height-2, 3) // -2 for top/bottom border
}

func (m Model) renderExplain() string {
	innerWidth := max(m.width-4, 20)
	visibleRows := m.explainVisibleRows()

	lines := m.explainLines()

	maxScroll := max(len(lines)-visibleRows, 0)
	if m.explainScroll > maxScroll {
		m.explainScroll = maxScroll
	}

	end := min(m.explainScroll+visibleRows, len(lines))
	visible := lines[m.explainScroll:end]

	// Highlight full lines first, then ANSI-aware slice for horizontal scroll.
	for i, line := range visible {
		visible[i] = ansi.Cut(highlight.Plan(line), m.explainHScroll, m.explainHScroll+innerWidth)
	}
	content := strings.Join(visible, "\n")

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
		title := " " + m.explainMode.String() + " "
		dashes := max(innerWidth-len([]rune(title)), 0)
		boxLines[0] = borderFg.Render("╭") +
			titleStyle.Render(title) +
			borderFg.Render(strings.Repeat("─", dashes)+"╮")
	}

	if n := len(boxLines); n > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		help := " q: back  j/k/h/l: scroll  c: copy  e/E: edit+explain "
		dashes := max(innerWidth-len([]rune(help)), 0)
		boxLines[n-1] = borderFg.Render("╰") +
			lipgloss.NewStyle().Faint(true).Render(help) +
			borderFg.Render(strings.Repeat("─", dashes)+"╯")
	}

	return strings.Join(boxLines, "\n")
}

func runExplain(client tapv1.TapServiceClient, mode explain.Mode, query string, args []string) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.Explain(context.Background(), &tapv1.ExplainRequest{
			Query:   query,
			Args:    args,
			Analyze: mode == explain.Analyze,
		})
		if err != nil {
			return explainResultMsg{err: err}
		}
		return explainResultMsg{plan: resp.GetPlan()}
	}
}
