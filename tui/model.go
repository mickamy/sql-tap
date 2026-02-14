package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/mickamy/sql-tap/clipboard"
	"github.com/mickamy/sql-tap/explain"
	tapv1 "github.com/mickamy/sql-tap/gen/tap/v1"
	"github.com/mickamy/sql-tap/proxy"
	"github.com/mickamy/sql-tap/query"
)

type viewMode int

const (
	viewList viewMode = iota
	viewInspect
	viewExplain
)

type rowKind int

const (
	rowEvent rowKind = iota
	rowTxSummary
)

type displayRow struct {
	kind     rowKind
	eventIdx int    // rowEvent: index into Model.events
	txID     string // rowTxSummary: transaction ID
	events   []int  // rowTxSummary: indices of all events in this tx (order preserved)
}

// Model is the Bubble Tea model for the sql-tap TUI.
type Model struct {
	target string
	client tapv1.TapServiceClient
	conn   *grpc.ClientConn
	stream tapv1.TapService_WatchClient

	events      []*tapv1.QueryEvent
	cursor      int // index into displayRows
	follow      bool
	width       int
	height      int
	err         error
	view        viewMode
	collapsed   map[string]bool
	displayRows []displayRow
	txColorMap  map[string]lipgloss.Color

	searchMode  bool
	searchQuery string

	inspectScroll  int
	explainPlan    string
	explainErr     error
	explainScroll  int
	explainHScroll int
	explainMode    explain.Mode
	explainQuery   string
	explainArgs    []string
}

// eventMsg carries a received QueryEvent from the gRPC stream.
type eventMsg struct{ Event *tapv1.QueryEvent }

// errMsg carries an error from the gRPC connection or stream.
type errMsg struct{ Err error }

type explainResultMsg struct {
	plan string
	err  error
}

// connectedMsg is sent after successfully establishing the gRPC Watch stream.
type connectedMsg struct {
	client tapv1.TapServiceClient
	conn   *grpc.ClientConn
	stream tapv1.TapService_WatchClient
}

// New creates a new Model targeting the given tapd server address.
func New(target string) Model {
	return Model{
		target:    target,
		follow:    true,
		collapsed: make(map[string]bool),
	}
}

// Init starts the gRPC connection.
func (m Model) Init() tea.Cmd {
	return connect(m.target)
}

func connect(target string) tea.Cmd {
	return func() tea.Msg {
		conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return errMsg{Err: fmt.Errorf("dial %s: %w", target, err)}
		}
		client := tapv1.NewTapServiceClient(conn)
		stream, err := client.Watch(context.Background(), &tapv1.WatchRequest{})
		if err != nil {
			_ = conn.Close()
			return errMsg{Err: fmt.Errorf("watch %s: %w", target, err)}
		}
		return connectedMsg{client: client, conn: conn, stream: stream}
	}
}

func recvEvent(stream tapv1.TapService_WatchClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := stream.Recv()
		if err != nil {
			return errMsg{Err: err}
		}
		return eventMsg{Event: resp.GetEvent()}
	}
}

// Update handles incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case connectedMsg:
		m.client = msg.client
		m.conn = msg.conn
		m.stream = msg.stream
		return m, recvEvent(msg.stream)

	case eventMsg:
		m.events = append(m.events, msg.Event)
		if m.view != viewList {
			return m, recvEvent(m.stream)
		}
		m.displayRows, m.txColorMap = rebuildDisplayRows(m.events, m.collapsed, m.searchQuery)
		if m.follow {
			m.cursor = max(len(m.displayRows)-1, 0)
		}
		return m, recvEvent(m.stream)

	case errMsg:
		m.err = msg.Err
		return m, nil

	case explainResultMsg:
		m.explainPlan = msg.plan
		m.explainErr = msg.err
		return m, nil

	case editorResultMsg:
		if msg.err != nil {
			m.view = viewExplain
			m.explainPlan = ""
			m.explainErr = msg.err
			m.explainScroll = 0
			m.explainHScroll = 0
			m.explainMode = msg.mode
			return m, nil
		}
		if msg.query == "" {
			return m, nil // cancelled
		}
		m.view = viewExplain
		m.explainPlan = ""
		m.explainErr = nil
		m.explainScroll = 0
		m.explainHScroll = 0
		m.explainMode = msg.mode
		m.explainQuery = msg.query
		m.explainArgs = msg.args
		return m, runExplain(m.client, msg.mode, msg.query, msg.args)

	case tea.KeyMsg:
		switch m.view {
		case viewInspect:
			return m.updateInspect(msg)
		case viewExplain:
			return m.updateExplain(msg)
		case viewList:
			return m.updateList(msg)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}
	return m, nil
}

// View renders the TUI.
func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	if m.err != nil {
		return friendlyError(m.err, m.width)
	}

	if len(m.events) == 0 {
		return "Waiting for queries..."
	}

	switch m.view {
	case viewInspect:
		return m.renderInspector()
	case viewExplain:
		return m.renderExplain()
	case viewList:
	}

	listHeight := max(m.height-12, 3)

	var footer string
	switch {
	case m.searchMode:
		footer = fmt.Sprintf("  / %s█", m.searchQuery)
	case m.searchQuery != "":
		footer = "  q: quit  j/k: navigate  space: toggle tx  enter: inspect" +
			"  c: copy query  C: copy with args  x/X: explain/analyze  e/E: edit+explain" +
			"  esc: clear filter"
	default:
		footer = "  q: quit  j/k: navigate  space: toggle tx  enter: inspect" +
			"  c: copy query  C: copy with args  x/X: explain/analyze  e/E: edit+explain" +
			"  /: search"
	}

	return strings.Join([]string{
		m.renderList(listHeight),
		m.renderPreview(),
		footer,
	}, "\n")
}

func rebuildDisplayRows(
	events []*tapv1.QueryEvent, collapsed map[string]bool, filter string,
) ([]displayRow, map[string]lipgloss.Color) {
	matchedEvents := matchingEvents(events, filter)

	// When filtering, show only matched events (flat, no tx grouping).
	if filter != "" {
		var rows []displayRow
		colorMap := make(map[string]lipgloss.Color)
		txCount := 0
		for i, ev := range events {
			if !matchedEvents[i] {
				continue
			}
			if txID := ev.GetTxId(); txID != "" {
				if _, ok := colorMap[txID]; !ok {
					colorMap[txID] = txColors[txCount%len(txColors)]
					txCount++
				}
			}
			rows = append(rows, displayRow{
				kind:     rowEvent,
				eventIdx: i,
			})
		}
		return rows, colorMap
	}

	var rows []displayRow
	seenTx := make(map[string]bool)
	colorMap := make(map[string]lipgloss.Color)
	txCount := 0

	for i := range events {
		ev := events[i]
		txID := ev.GetTxId()

		switch {
		case txID != "" && proxy.Op(ev.GetOp()) == proxy.OpBegin && !seenTx[txID]:
			seenTx[txID] = true
			colorMap[txID] = txColors[txCount%len(txColors)]
			txCount++
			// Collect all events with this txID.
			var indices []int
			for j := range events {
				if events[j].GetTxId() == txID {
					indices = append(indices, j)
				}
			}
			rows = append(rows, displayRow{
				kind:   rowTxSummary,
				txID:   txID,
				events: indices,
			})
			if !collapsed[txID] {
				for _, j := range indices {
					rows = append(rows, displayRow{
						kind:     rowEvent,
						eventIdx: j,
					})
				}
			}
		case txID != "" && seenTx[txID]:
			// Already handled by summary — skip.
		default:
			// Non-tx event.
			rows = append(rows, displayRow{
				kind:     rowEvent,
				eventIdx: i,
			})
		}
	}

	return rows, colorMap
}

// matchingEvents returns a set of event indices whose query contains the filter (case-insensitive).
// If filter is empty, all events match.
func matchingEvents(events []*tapv1.QueryEvent, filter string) map[int]bool {
	matched := make(map[int]bool, len(events))
	if filter == "" {
		for i := range events {
			matched[i] = true
		}
		return matched
	}

	lower := strings.ToLower(filter)
	for i, ev := range events {
		if strings.Contains(strings.ToLower(ev.GetQuery()), lower) {
			matched[i] = true
		}
	}
	return matched
}

// txQueryCount returns the number of non-lifecycle events in a tx.
// Lifecycle ops (Begin, Commit, Rollback, Bind, Prepare) are skipped.
func (m Model) txQueryCount(indices []int) int {
	n := 0
	for _, idx := range indices {
		switch proxy.Op(m.events[idx].GetOp()) {
		case proxy.OpBegin, proxy.OpCommit, proxy.OpRollback, proxy.OpBind, proxy.OpPrepare:
		case proxy.OpQuery, proxy.OpExec, proxy.OpExecute:
			n++
		}
	}
	return n
}

// txWallDuration returns the wall-clock duration from the first event's StartTime
// to the last event's StartTime + Duration.
func (m Model) txWallDuration(indices []int) time.Duration {
	if len(indices) == 0 {
		return 0
	}
	first := m.events[indices[0]]
	last := m.events[indices[len(indices)-1]]

	start := first.GetStartTime().AsTime()
	end := last.GetStartTime().AsTime().Add(last.GetDuration().AsDuration())
	return end.Sub(start)
}

// cursorTxID returns the tx ID for the current cursor row, or "" if not tx-related.
func (m Model) cursorTxID() string {
	if m.cursor < 0 || m.cursor >= len(m.displayRows) {
		return ""
	}
	dr := m.displayRows[m.cursor]
	switch dr.kind {
	case rowTxSummary:
		return dr.txID
	case rowEvent:
		return m.events[dr.eventIdx].GetTxId()
	}
	return ""
}

// isTxChild returns true if the display row at index i is an event that belongs
// to a tx summary (i.e. the preceding summary row exists).
func (m Model) isTxChild(drIdx int) bool {
	if drIdx < 0 || drIdx >= len(m.displayRows) {
		return false
	}
	dr := m.displayRows[drIdx]
	if dr.kind != rowEvent {
		return false
	}
	ev := m.events[dr.eventIdx]
	return ev.GetTxId() != ""
}

// cursorEvent returns the QueryEvent at the cursor, or nil for tx summary rows.
func (m Model) cursorEvent() *tapv1.QueryEvent {
	if m.cursor < 0 || m.cursor >= len(m.displayRows) {
		return nil
	}
	dr := m.displayRows[m.cursor]
	if dr.kind != rowEvent {
		return nil
	}
	return m.events[dr.eventIdx]
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searchMode {
		return m.updateSearch(msg)
	}

	switch msg.String() {
	case "q", "ctrl+c":
		if m.conn != nil {
			_ = m.conn.Close()
		}
		return m, tea.Quit
	case "enter":
		if len(m.displayRows) > 0 {
			m.view = viewInspect
			m.inspectScroll = 0
		}
		return m, nil
	case "x", "X":
		mode := explain.Explain
		if msg.String() == "X" {
			mode = explain.Analyze
		}
		return m.startExplain(mode)
	case "e", "E":
		mode := explain.Explain
		if msg.String() == "E" {
			mode = explain.Analyze
		}
		return m.startEditExplain(mode)
	case "c", "C":
		return m.copyQuery(msg.String() == "C"), nil
	case "/":
		m.searchMode = true
		m.searchQuery = ""
		return m, nil
	case "esc":
		if m.searchQuery != "" {
			m.searchQuery = ""
			m.displayRows, m.txColorMap = rebuildDisplayRows(m.events, m.collapsed, "")
			m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		}
		return m, nil
	case " ":
		if txID := m.cursorTxID(); txID != "" {
			m.collapsed[txID] = !m.collapsed[txID]
			m.displayRows, m.txColorMap = rebuildDisplayRows(m.events, m.collapsed, m.searchQuery)
			for i, r := range m.displayRows {
				if r.kind == rowTxSummary && r.txID == txID {
					m.cursor = i
					break
				}
			}
		}
		return m, nil
	case "j", "down":
		if len(m.displayRows) > 0 && m.cursor < len(m.displayRows)-1 {
			m.cursor++
		}
		if len(m.displayRows) > 0 && m.cursor == len(m.displayRows)-1 {
			m.follow = true
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.follow = false
		}
		return m, nil
	}
	return m, nil
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.searchMode = false
		return m, nil
	case "esc":
		m.searchMode = false
		m.searchQuery = ""
		m.displayRows, m.txColorMap = rebuildDisplayRows(m.events, m.collapsed, "")
		m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		return m, nil
	case "backspace":
		if len(m.searchQuery) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.searchQuery)
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-size]
			m.displayRows, m.txColorMap = rebuildDisplayRows(m.events, m.collapsed, m.searchQuery)
			m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		}
		return m, nil
	case "ctrl+c":
		if m.conn != nil {
			_ = m.conn.Close()
		}
		return m, tea.Quit
	case "up", "down":
		return m.navigateCursor(msg.String()), nil
	}

	// Ignore non-printable keys.
	r := msg.Runes
	if len(r) == 0 {
		return m, nil
	}

	m.searchQuery += string(r)
	m.displayRows, m.txColorMap = rebuildDisplayRows(m.events, m.collapsed, m.searchQuery)
	m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
	return m, nil
}

func (m Model) navigateCursor(key string) Model {
	switch key {
	case "up":
		if m.cursor > 0 {
			m.cursor--
			m.follow = false
		}
	case "down":
		if len(m.displayRows) > 0 && m.cursor < len(m.displayRows)-1 {
			m.cursor++
		}
		if len(m.displayRows) > 0 && m.cursor == len(m.displayRows)-1 {
			m.follow = true
		}
	}
	return m
}

func (m Model) copyQuery(withArgs bool) Model {
	ev := m.cursorEvent()
	if ev == nil || ev.GetQuery() == "" {
		return m
	}
	text := ev.GetQuery()
	if withArgs {
		text = query.Bind(text, ev.GetArgs())
	}
	_ = clipboard.Copy(context.Background(), text)
	return m
}

func (m Model) startEditExplain(mode explain.Mode) (tea.Model, tea.Cmd) {
	ev := m.cursorEvent()
	if ev == nil || ev.GetQuery() == "" || isLifecycleOp(ev) {
		return m, nil
	}

	return m, openEditor(ev.GetQuery(), ev.GetArgs(), mode)
}

func isLifecycleOp(ev *tapv1.QueryEvent) bool {
	switch proxy.Op(ev.GetOp()) {
	case proxy.OpBegin, proxy.OpCommit, proxy.OpRollback:
		return true
	case proxy.OpQuery, proxy.OpExec, proxy.OpPrepare, proxy.OpBind, proxy.OpExecute:
	}
	return false
}

func (m Model) startExplain(mode explain.Mode) (tea.Model, tea.Cmd) {
	ev := m.cursorEvent()
	if ev == nil || ev.GetQuery() == "" || isLifecycleOp(ev) {
		return m, nil
	}

	m.view = viewExplain
	m.explainPlan = ""
	m.explainErr = nil
	m.explainScroll = 0
	m.explainHScroll = 0
	m.explainMode = mode
	m.explainQuery = ev.GetQuery()
	m.explainArgs = ev.GetArgs()
	return m, runExplain(m.client, mode, ev.GetQuery(), ev.GetArgs())
}
