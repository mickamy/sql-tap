package postgres

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	pgproto "github.com/jackc/pgproto3/v2"

	"github.com/mickamy/sql-tap/proxy"
)

// encoder is satisfied by both FrontendMessage and BackendMessage.
type encoder interface {
	Encode(dst []byte) ([]byte, error)
}

// conn manages bidirectional relay and protocol parsing for a single connection.
type conn struct {
	client   *pgproto.Backend  // reads FrontendMessages from client
	upstream *pgproto.Frontend // reads BackendMessages from upstream

	clientConn   net.Conn
	upstreamConn net.Conn
	events       chan<- proxy.Event

	// Extended query state.
	preparedStmts map[string]string // stmt name -> query
	lastParse     string            // query from most recent Parse
	lastBindArgs  []string          // args from most recent Bind
	lastBindStmt  string            // stmt name from most recent Bind
	executeStart  time.Time         // when Execute started

	// Transaction tracking.
	activeTxID string
	nextID     uint64
}

func newConn(clientConn, upstreamConn net.Conn, events chan<- proxy.Event) *conn {
	return &conn{
		clientConn:    clientConn,
		upstreamConn:  upstreamConn,
		events:        events,
		preparedStmts: make(map[string]string),
	}
}

func (c *conn) generateID() string {
	c.nextID++
	return strconv.FormatUint(c.nextID, 10)
}

// encodeAndWrite encodes a protocol message and writes it to dst.
func encodeAndWrite(dst net.Conn, msg encoder) error {
	buf, err := msg.Encode(nil)
	if err != nil {
		return fmt.Errorf("postgres: encode: %w", err)
	}
	if _, err := dst.Write(buf); err != nil {
		return fmt.Errorf("postgres: write: %w", err)
	}
	return nil
}

// relay handles the startup phase and then enters bidirectional message relay.
func (c *conn) relay(ctx context.Context) error {
	if err := c.relayStartup(); err != nil {
		return fmt.Errorf("postgres: startup: %w", err)
	}

	errCh := make(chan error, 2)

	go func() { errCh <- c.relayClientToUpstream(ctx) }()
	go func() { errCh <- c.relayUpstreamToClient(ctx) }()

	// Wait for the first goroutine to finish (connection closed or error).
	err := <-errCh
	// Close both sides to unblock the other goroutine.
	_ = c.clientConn.Close()
	_ = c.upstreamConn.Close()
	// Wait for the second goroutine.
	<-errCh

	return err
}

const (
	sslRequestCode    = 80877103
	gssEncRequestCode = 80877104

	authTypeOk        = 0
	authTypeSASLFinal = 12
)

// relayStartup handles the startup/auth phase using raw byte relay to avoid
// re-encoding issues with SCRAM and other auth mechanisms. Protocol parsers
// (Backend/Frontend) are created only after auth completes.
func (c *conn) relayStartup() error {
	// Handle SSLRequest / GSSEncRequest, then forward the real StartupMessage.
	for {
		raw, err := readStartupRaw(c.clientConn)
		if err != nil {
			return fmt.Errorf("postgres: read startup: %w", err)
		}

		// SSLRequest and GSSEncRequest are 8-byte messages with a specific code.
		if len(raw) == 8 {
			code := binary.BigEndian.Uint32(raw[4:])
			switch code {
			case sslRequestCode:
				if _, err := c.clientConn.Write([]byte{'N'}); err != nil {
					return fmt.Errorf("postgres: decline ssl: %w", err)
				}
				continue
			case gssEncRequestCode:
				if _, err := c.clientConn.Write([]byte{'N'}); err != nil {
					return fmt.Errorf("postgres: decline gss: %w", err)
				}
				continue
			}
		}

		if _, err := c.upstreamConn.Write(raw); err != nil {
			return fmt.Errorf("postgres: send startup: %w", err)
		}
		break
	}

	// Relay auth messages as raw bytes until ReadyForQuery.
	for {
		msg, err := readMessageRaw(c.upstreamConn)
		if err != nil {
			return fmt.Errorf("postgres: receive auth: %w", err)
		}

		if _, err := c.clientConn.Write(msg); err != nil {
			return fmt.Errorf("postgres: send auth: %w", err)
		}

		switch msg[0] {
		case 'Z': // ReadyForQuery — auth complete.
			c.client = pgproto.NewBackend(pgproto.NewChunkReader(c.clientConn), c.clientConn)
			c.upstream = pgproto.NewFrontend(pgproto.NewChunkReader(c.upstreamConn), c.upstreamConn)
			return nil
		case 'E': // ErrorResponse
			return errors.New("postgres: auth error from upstream")
		case 'R': // Authentication message
			if len(msg) >= 9 {
				authType := binary.BigEndian.Uint32(msg[5:9])
				// AuthenticationOk and AuthenticationSASLFinal require no client response.
				if authType != authTypeOk && authType != authTypeSASLFinal {
					resp, err := readMessageRaw(c.clientConn)
					if err != nil {
						return fmt.Errorf("postgres: receive auth response: %w", err)
					}
					if _, err := c.upstreamConn.Write(resp); err != nil {
						return fmt.Errorf("postgres: send auth response: %w", err)
					}
				}
			}
		}
	}
}

// readStartupRaw reads a startup-format message (no type byte): 4-byte length + payload.
func readStartupRaw(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("postgres: read startup header: %w", err)
	}
	msgLen := binary.BigEndian.Uint32(hdr[:])
	if msgLen < 4 {
		return nil, errors.New("postgres: invalid startup message length")
	}
	buf := make([]byte, msgLen)
	copy(buf, hdr[:])
	if _, err := io.ReadFull(r, buf[4:]); err != nil {
		return nil, fmt.Errorf("postgres: read startup payload: %w", err)
	}
	return buf, nil
}

// readMessageRaw reads a regular protocol message: 1-byte type + 4-byte length + payload.
func readMessageRaw(r io.Reader) ([]byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("postgres: read message header: %w", err)
	}
	msgLen := binary.BigEndian.Uint32(hdr[1:5])
	buf := make([]byte, 1+msgLen)
	copy(buf, hdr[:])
	if _, err := io.ReadFull(r, buf[5:]); err != nil {
		return nil, fmt.Errorf("postgres: read message payload: %w", err)
	}
	return buf, nil
}

// relayClientToUpstream reads messages from the client, captures info, and forwards to upstream.
func (c *conn) relayClientToUpstream(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("postgres: client relay: %w", ctx.Err())
		}

		msg, err := c.client.Receive()
		if err != nil {
			if isClosedErr(err) {
				return nil
			}
			return fmt.Errorf("postgres: receive from client: %w", err)
		}

		c.captureClientMsg(msg)

		if err := encodeAndWrite(c.upstreamConn, msg); err != nil {
			if isClosedErr(err) {
				return nil
			}
			return fmt.Errorf("postgres: send to upstream: %w", err)
		}
	}
}

// relayUpstreamToClient reads messages from upstream, captures info, and forwards to client.
func (c *conn) relayUpstreamToClient(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("postgres: upstream relay: %w", ctx.Err())
		}

		msg, err := c.upstream.Receive()
		if err != nil {
			if isClosedErr(err) {
				return nil
			}
			return fmt.Errorf("postgres: receive from upstream: %w", err)
		}

		c.captureUpstreamMsg(msg)

		if err := encodeAndWrite(c.clientConn, msg); err != nil {
			if isClosedErr(err) {
				return nil
			}
			return fmt.Errorf("postgres: send to client: %w", err)
		}
	}
}

func (c *conn) captureClientMsg(msg pgproto.FrontendMessage) {
	switch m := msg.(type) {
	case *pgproto.Query:
		c.handleSimpleQuery(m)
	case *pgproto.Parse:
		c.handleParse(m)
	case *pgproto.Bind:
		c.handleBind(m)
	case *pgproto.Execute:
		c.handleExecute()
	}
}

func (c *conn) captureUpstreamMsg(msg pgproto.BackendMessage) {
	switch m := msg.(type) {
	case *pgproto.CommandComplete:
		c.handleCommandComplete(m)
	case *pgproto.ErrorResponse:
		c.handleErrorResponse(m)
	}
}

func (c *conn) handleSimpleQuery(m *pgproto.Query) {
	q := m.String
	r := c.detectTx(q, proxy.OpQuery)

	ev := proxy.Event{
		ID:        c.generateID(),
		Op:        r.op,
		Query:     q,
		StartTime: time.Now(),
		TxID:      r.txID,
	}
	c.emitEvent(ev)
}

func (c *conn) handleParse(m *pgproto.Parse) {
	c.lastParse = m.Query
	if m.Name != "" {
		c.preparedStmts[m.Name] = m.Query
	}
}

func (c *conn) handleBind(m *pgproto.Bind) {
	c.lastBindStmt = m.PreparedStatement
	c.lastBindArgs = make([]string, len(m.Parameters))
	for i, p := range m.Parameters {
		c.lastBindArgs[i] = string(p)
	}
}

func (c *conn) handleExecute() {
	q := c.lastParse
	if c.lastBindStmt != "" {
		if stored, ok := c.preparedStmts[c.lastBindStmt]; ok {
			q = stored
		}
	}

	r := c.detectTx(q, proxy.OpExecute)
	c.executeStart = time.Now()

	ev := proxy.Event{
		ID:        c.generateID(),
		Op:        r.op,
		Query:     q,
		Args:      c.lastBindArgs,
		StartTime: c.executeStart,
		TxID:      r.txID,
	}
	c.emitEvent(ev)
}

func (c *conn) handleCommandComplete(m *pgproto.CommandComplete) {
	rows := parseRowsAffected(string(m.CommandTag))
	_ = rows // rows info is available but we already emitted the event at request time
}

func (c *conn) handleErrorResponse(m *pgproto.ErrorResponse) {
	_ = m // error info is available but we already emitted the event at request time
}

type txDetectResult struct {
	txID string
	op   proxy.Op // overridden Op for BEGIN/COMMIT/ROLLBACK; zero means keep original
}

// detectTx updates transaction state and returns the txID and Op to use for the current event.
func (c *conn) detectTx(query string, defaultOp proxy.Op) txDetectResult {
	upper := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(upper, "BEGIN"):
		c.activeTxID = uuid.New().String()
		return txDetectResult{txID: c.activeTxID, op: proxy.OpBegin}
	case strings.HasPrefix(upper, "COMMIT"):
		prev := c.activeTxID
		c.activeTxID = ""
		return txDetectResult{txID: prev, op: proxy.OpCommit}
	case strings.HasPrefix(upper, "ROLLBACK"):
		prev := c.activeTxID
		c.activeTxID = ""
		return txDetectResult{txID: prev, op: proxy.OpRollback}
	}
	return txDetectResult{txID: c.activeTxID, op: defaultOp}
}

func (c *conn) emitEvent(ev proxy.Event) {
	select {
	case c.events <- ev:
	default:
		// channel full; drop
	}
}

// parseRowsAffected extracts the row count from a CommandComplete tag.
// e.g. "INSERT 0 5" -> 5, "SELECT 3" -> 3, "UPDATE 10" -> 10.
func parseRowsAffected(tag string) int64 {
	parts := strings.Split(tag, " ")
	if len(parts) == 0 {
		return 0
	}
	n, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	return n
}

func isClosedErr(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return netErr.Err.Error() == "use of closed network connection"
	}
	return strings.Contains(err.Error(), "closed")
}
