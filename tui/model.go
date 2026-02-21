package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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
	viewAnalytics
)

type sortMode int

const (
	sortChronological sortMode = iota
	sortDuration
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

	searchMode   bool
	searchQuery  string
	searchCursor int
	filterMode   bool
	filterQuery  string
	filterCursor int
	sortMode     sortMode

	writeMode      bool
	wroteMessage   string
	alertSeq       int
	pendingBracket bool

	inspectScroll  int
	explainPlan    string
	explainErr     error
	explainScroll  int
	explainHScroll int
	explainMode    explain.Mode
	explainQuery   string
	explainArgs    []string

	analyticsRows     []analyticsRow
	analyticsCursor   int
	analyticsHScroll  int
	analyticsSortMode analyticsSortMode
}

// eventMsg carries a received QueryEvent from the gRPC stream.
type eventMsg struct{ Event *tapv1.QueryEvent }

// errMsg carries an error from the gRPC connection or stream.
type errMsg struct{ Err error }

type explainResultMsg struct {
	plan string
	err  error
}

type exportResultMsg struct {
	path string
	err  error
}

type clearAlertMsg struct{ seq int }

const alertDuration = 3 * time.Second

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

		if msg.Event.GetNPlus_1() || msg.Event.GetSlowQuery() {
			q := msg.Event.GetQuery()
			if len(q) > 60 {
				q = q[:57] + "..."
			}
			label := "N+1 detected: "
			if msg.Event.GetSlowQuery() && !msg.Event.GetNPlus_1() {
				label = "Slow query: "
			}
			m, alertCmd := m.showAlert(label + q)
			if m.view != viewList {
				return m, tea.Batch(alertCmd, recvEvent(m.stream))
			}
			m = m.rebuild()
			if m.follow {
				m.cursor = max(len(m.displayRows)-1, 0)
			}
			return m, tea.Batch(alertCmd, recvEvent(m.stream))
		}

		if m.view != viewList {
			return m, recvEvent(m.stream)
		}
		m = m.rebuild()
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
			return m, nil // canceled
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

	case exportResultMsg:
		alertMsg := "wrote: ./" + msg.path
		if msg.err != nil {
			alertMsg = "write error: " + msg.err.Error()
		}
		m, cmd := m.showAlert(alertMsg)
		return m, cmd

	case clearAlertMsg:
		if msg.seq == m.alertSeq {
			m.wroteMessage = ""
		}
		return m, nil

	case tea.KeyMsg:
		m.wroteMessage = ""
		switch m.view {
		case viewInspect:
			return m.updateInspect(msg)
		case viewExplain:
			return m.updateExplain(msg)
		case viewAnalytics:
			return m.updateAnalytics(msg)
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

	var view string
	switch m.view {
	case viewInspect:
		view = m.renderInspector()
	case viewExplain:
		view = m.renderExplain()
	case viewAnalytics:
		view = m.renderAnalytics()
	case viewList:
		var footer string
		switch {
		case m.searchMode:
			footer = "  / " + renderInputWithCursor(m.searchQuery, m.searchCursor)
		case m.filterMode:
			footer = "  filter: " + renderInputWithCursor(m.filterQuery, m.filterCursor)
		case m.writeMode:
			footer = "  write: [j]son [m]arkdown"
		default:
			items := []string{
				"q: quit", "j/k: navigate", "space: toggle tx",
				"enter: inspect", "a: analytics",
				"c/C: copy", "x/X: explain",
				"e/E: edit+explain", "/: search", "f: filter", "s: sort",
				"w: write",
			}
			footer = wrapFooterItems(items, m.width)
			if m.filterQuery != "" {
				footer += "\n  " + fmt.Sprintf("[filter: %s]", describeFilter(m.filterQuery))
			}
			if m.searchQuery != "" || m.filterQuery != "" {
				footer += "  esc: clear"
			}
			if m.sortMode == sortDuration {
				footer += "  [sorted: duration]"
			}
		}

		footerLines := strings.Count(footer, "\n") + 1
		listHeight := m.listHeight(footerLines)

		view = strings.Join([]string{
			m.renderList(listHeight),
			m.renderPreview(),
			footer,
		}, "\n")
	}

	if m.wroteMessage != "" {
		view = overlayAlert(view, m.wroteMessage, m.width)
	}

	return view
}

func (m Model) listHeight(footerLines int) int {
	// 12 = header border (1) + preview box (~8-9 lines) + footer (1) + padding.
	// Adjust by extra footer lines beyond the default 1.
	extra := max(footerLines-1, 0)
	return max(m.height-12-extra, 3)
}

// rebuild wraps rebuildDisplayRows for convenience.
func (m Model) rebuild() Model {
	m.displayRows, m.txColorMap = m.rebuildDisplayRows()
	return m
}

func (m Model) rebuildDisplayRows() ([]displayRow, map[string]lipgloss.Color) {
	matchedEvents := matchingEventsFiltered(m.events, m.filterQuery, m.searchQuery)

	active := m.filterQuery != "" || m.searchQuery != ""
	// When filtering or sorting by duration, show flat list (no tx grouping).
	if active || m.sortMode == sortDuration {
		var rows []displayRow
		colorMap := make(map[string]lipgloss.Color)
		txCount := 0
		for i, ev := range m.events {
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
		if m.sortMode == sortDuration {
			sort.Slice(rows, func(a, b int) bool {
				da := m.events[rows[a].eventIdx].GetDuration().AsDuration()
				db := m.events[rows[b].eventIdx].GetDuration().AsDuration()
				return da > db // slowest first
			})
		}
		return rows, colorMap
	}

	var rows []displayRow
	seenTx := make(map[string]bool)
	colorMap := make(map[string]lipgloss.Color)
	txCount := 0

	for i := range m.events {
		ev := m.events[i]
		txID := ev.GetTxId()

		switch {
		case txID != "" && proxy.Op(ev.GetOp()) == proxy.OpBegin && !seenTx[txID]:
			seenTx[txID] = true
			colorMap[txID] = txColors[txCount%len(txColors)]
			txCount++
			// Collect all events with this txID.
			var indices []int
			for j := range m.events {
				if m.events[j].GetTxId() == txID {
					indices = append(indices, j)
				}
			}
			rows = append(rows, displayRow{
				kind:   rowTxSummary,
				txID:   txID,
				events: indices,
			})
			if !m.collapsed[txID] {
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

// matchingEventsFiltered returns a set of event indices that pass both the structured
// filter (filterQuery) and the text search (searchQuery). Either may be empty.
func matchingEventsFiltered(events []*tapv1.QueryEvent, filterQuery, searchQuery string) map[int]bool {
	matched := make(map[int]bool, len(events))

	var filterConds []filterCondition
	if filterQuery != "" {
		filterConds = parseFilter(filterQuery)
	}
	searchLower := strings.ToLower(searchQuery)

	for i, ev := range events {
		if len(filterConds) > 0 && !matchAllConditions(ev, filterConds) {
			continue
		}
		if searchLower != "" && !strings.Contains(strings.ToLower(ev.GetQuery()), searchLower) {
			continue
		}
		matched[i] = true
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
	if m.filterMode {
		return m.updateFilter(msg)
	}
	if m.writeMode {
		return m.updateWrite(msg)
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
		return m.startExplain(explainModeFromKey(msg.String()))
	case "e", "E":
		return m.startEditExplain(explainModeFromKey(msg.String()))
	case "c", "C":
		return m.copyQuery(msg.String() == "C")
	case "/":
		m.searchMode = true
		m.searchQuery = ""
		m.searchCursor = 0
		return m, nil
	case "f":
		m.filterMode = true
		m.filterQuery = ""
		m.filterCursor = 0
		return m, nil
	case "w":
		m.writeMode = true
		return m, nil
	case "s":
		return m.toggleSort(), nil
	case "a":
		return m.enterAnalytics(), nil
	case "esc":
		return m.clearFilter(), nil
	case " ":
		return m.toggleTx(), nil
	case "j", "down":
		return m.navigateCursor(msg.String()), nil
	case "k", "up":
		return m.navigateCursor(msg.String()), nil
	case "ctrl+d", "pgdown":
		return m.pageScroll(msg.String()), nil
	case "ctrl+u", "pgup":
		return m.pageScroll(msg.String()), nil
	}
	return m, nil
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.searchMode = false
		m.pendingBracket = false
		m = m.rebuild()
		m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		return m, nil
	case "esc":
		m.searchMode = false
		m.searchQuery = ""
		m.pendingBracket = false
		m = m.rebuild()
		m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		return m, nil
	case "backspace":
		if m.searchCursor > 0 {
			runes := []rune(m.searchQuery)
			m.searchQuery = string(runes[:m.searchCursor-1]) + string(runes[m.searchCursor:])
			m.searchCursor--
			m = m.rebuild()
			m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		}
		return m, nil
	case "ctrl+c":
		if m.conn != nil {
			_ = m.conn.Close()
		}
		return m, tea.Quit
	case "left":
		if m.searchCursor > 0 {
			m.searchCursor--
		}
		return m, nil
	case "right":
		if m.searchCursor < len([]rune(m.searchQuery)) {
			m.searchCursor++
		}
		return m, nil
	case "up", "down":
		return m.navigateCursor(msg.String()), nil
	}

	if len(msg.Runes) == 0 {
		return m, nil
	}

	var r []rune
	m, r = m.filterInputRunes(msg.Runes)
	if len(r) == 0 {
		return m, nil
	}

	runes := []rune(m.searchQuery)
	m.searchQuery = string(runes[:m.searchCursor]) + string(r) + string(runes[m.searchCursor:])
	m.searchCursor += len(r)
	m = m.rebuild()
	m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
	return m, nil
}

func (m Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.filterMode = false
		m.pendingBracket = false
		m = m.rebuild()
		m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		return m, nil
	case "esc":
		m.filterMode = false
		m.filterQuery = ""
		m.pendingBracket = false
		m = m.rebuild()
		m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		return m, nil
	case "backspace":
		if m.filterCursor > 0 {
			runes := []rune(m.filterQuery)
			m.filterQuery = string(runes[:m.filterCursor-1]) + string(runes[m.filterCursor:])
			m.filterCursor--
			m = m.rebuild()
			m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		}
		return m, nil
	case "ctrl+c":
		if m.conn != nil {
			_ = m.conn.Close()
		}
		return m, tea.Quit
	case "left":
		if m.filterCursor > 0 {
			m.filterCursor--
		}
		return m, nil
	case "right":
		if m.filterCursor < len([]rune(m.filterQuery)) {
			m.filterCursor++
		}
		return m, nil
	case "up", "down":
		return m.navigateCursor(msg.String()), nil
	}

	if len(msg.Runes) == 0 {
		return m, nil
	}

	var r []rune
	m, r = m.filterInputRunes(msg.Runes)
	if len(r) == 0 {
		return m, nil
	}

	runes := []rune(m.filterQuery)
	m.filterQuery = string(runes[:m.filterCursor]) + string(r) + string(runes[m.filterCursor:])
	m.filterCursor += len(r)
	m = m.rebuild()
	m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
	return m, nil
}

func (m Model) updateWrite(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.writeMode = false
	switch msg.String() {
	case "j":
		return m, m.runExport(exportJSON)
	case "m":
		return m, m.runExport(exportMarkdown)
	}
	return m, nil
}

func (m Model) runExport(format exportFormat) tea.Cmd {
	events := make([]*tapv1.QueryEvent, len(m.events))
	copy(events, m.events)
	filterQuery := m.filterQuery
	searchQuery := m.searchQuery
	return func() tea.Msg {
		path, err := writeExport(
			events, filterQuery, searchQuery, format, "",
		)
		return exportResultMsg{path: path, err: err}
	}
}

func (m Model) toggleTx() Model {
	txID := m.cursorTxID()
	if txID == "" {
		return m
	}
	m.collapsed[txID] = !m.collapsed[txID]
	m = m.rebuild()
	for i, r := range m.displayRows {
		if r.kind == rowTxSummary && r.txID == txID {
			m.cursor = i
			break
		}
	}
	return m
}

func (m Model) pageScroll(key string) Model {
	half := max(m.listHeight(1)/2, 1)
	switch key {
	case "ctrl+d", "pgdown":
		m.cursor = min(m.cursor+half, max(len(m.displayRows)-1, 0))
		if len(m.displayRows) > 0 && m.cursor == len(m.displayRows)-1 {
			m.follow = true
		}
	case "ctrl+u", "pgup":
		m.cursor = max(m.cursor-half, 0)
		m.follow = false
	}
	return m
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

// filterInputRunes works around a bubbletea v1 limitation where arrow-key
// escape sequences (ESC [ A/B/C/D/F/H) can be split across read() calls.
// When that happens, ESC is consumed as KeyEscape and the remaining bytes
// arrive as rune input ("[D", etc.). This filter buffers a standalone "["
// and discards it together with a following CSI final byte, preventing
// garbage characters from being inserted into search/filter text.
//
// Limitation: the literal two-character sequence "[A", "[B", "[C", "[D",
// "[F", or "[H" cannot be typed in search/filter input.
func (m Model) filterInputRunes(r []rune) (Model, []rune) {
	// "[D" arrived as a single batch of runes.
	if len(r) >= 2 && r[0] == '[' {
		m.pendingBracket = false
		return m, nil
	}
	// Previous event was a standalone "["; check if this is a CSI final byte.
	if m.pendingBracket {
		m.pendingBracket = false
		if len(r) == 1 {
			switch r[0] {
			case 'A', 'B', 'C', 'D', 'F', 'H':
				return m, nil
			}
		}
		// Not a CSI byte — emit the buffered "[" plus current runes.
		return m, append([]rune{'['}, r...)
	}
	// Buffer a standalone "[" to check the next event.
	if len(r) == 1 && r[0] == '[' {
		m.pendingBracket = true
		return m, nil
	}
	return m, r
}

func (m Model) copyQuery(withArgs bool) (Model, tea.Cmd) {
	ev := m.cursorEvent()
	if ev == nil || ev.GetQuery() == "" {
		return m, nil
	}
	text := ev.GetQuery()
	if withArgs {
		text = query.Bind(text, ev.GetArgs())
	}
	_ = clipboard.Copy(context.Background(), text)
	return m.showAlert("copied!")
}

func (m Model) showAlert(msg string) (Model, tea.Cmd) {
	m.alertSeq++
	m.wroteMessage = msg
	seq := m.alertSeq
	return m, tea.Tick(alertDuration, func(time.Time) tea.Msg {
		return clearAlertMsg{seq: seq}
	})
}

func (m Model) toggleSort() Model {
	switch m.sortMode {
	case sortChronological:
		m.sortMode = sortDuration
		m.follow = false
	case sortDuration:
		m.sortMode = sortChronological
	}
	m = m.rebuild()
	m.cursor = 0
	return m
}

func (m Model) enterAnalytics() Model {
	m.analyticsRows = m.buildAnalyticsRows()
	sortAnalyticsRows(m.analyticsRows, m.analyticsSortMode)
	m.analyticsCursor = 0
	m.analyticsHScroll = 0
	m.view = viewAnalytics
	return m
}

func (m Model) clearFilter() Model {
	changed := false
	if m.searchQuery != "" {
		m.searchQuery = ""
		changed = true
	}
	if m.filterQuery != "" {
		m.filterQuery = ""
		changed = true
	}
	if changed {
		m = m.rebuild()
		m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
	}
	return m
}

func explainModeFromKey(key string) explain.Mode {
	switch key {
	case "X", "E":
		return explain.Analyze
	}
	return explain.Explain
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
