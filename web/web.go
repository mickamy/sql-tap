package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/mickamy/sql-tap/broker"
	"github.com/mickamy/sql-tap/explain"
	"github.com/mickamy/sql-tap/proxy"
)

//go:embed static
var staticFS embed.FS

// Server serves the sql-tap web UI and API endpoints.
type Server struct {
	httpServer *http.Server
	broker     *broker.Broker
	explain    *explain.Client
}

// New creates a new web Server backed by the given Broker.
// explainClient may be nil if EXPLAIN is not configured.
func New(b *broker.Broker, explainClient *explain.Client) *Server {
	s := &Server{
		broker:  b,
		explain: explainClient,
	}

	mux := http.NewServeMux()

	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /api/events", s.handleSSE)
	mux.HandleFunc("POST /api/explain", s.handleExplain)

	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Serve starts the HTTP server on the given listener.
func (s *Server) Serve(lis net.Listener) error {
	if err := s.httpServer.Serve(lis); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("web: serve: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("web: shutdown: %w", err)
	}
	return nil
}

// Handler returns the HTTP handler for testing.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

type eventJSON struct {
	ID              string   `json:"id"`
	Op              string   `json:"op"`
	Query           string   `json:"query"`
	Args            []string `json:"args"`
	StartTime       string   `json:"start_time"`
	DurationMs      float64  `json:"duration_ms"`
	RowsAffected    int64    `json:"rows_affected"`
	Error           string   `json:"error,omitempty"`
	TxID            string   `json:"tx_id,omitempty"`
	NPlus1          bool     `json:"n_plus_1,omitempty"`
	SlowQuery       bool     `json:"slow_query,omitempty"`
	NormalizedQuery string   `json:"normalized_query,omitempty"`
}

func eventToJSON(ev proxy.Event) eventJSON {
	args := make([]string, len(ev.Args))
	copy(args, ev.Args)
	return eventJSON{
		ID:              ev.ID,
		Op:              ev.Op.String(),
		Query:           ev.Query,
		Args:            args,
		StartTime:       ev.StartTime.Format(time.RFC3339Nano),
		DurationMs:      float64(ev.Duration.Microseconds()) / 1000,
		RowsAffected:    ev.RowsAffected,
		Error:           ev.Error,
		TxID:            ev.TxID,
		NPlus1:          ev.NPlus1,
		SlowQuery:       ev.SlowQuery,
		NormalizedQuery: ev.NormalizedQuery,
	}
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher.Flush() // send headers immediately

	ch, unsub := s.broker.Subscribe()
	defer unsub()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(eventToJSON(ev))
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

type explainRequest struct {
	Query   string   `json:"query"`
	Args    []string `json:"args"`
	Analyze bool     `json:"analyze"`
}

type explainResponse struct {
	Plan  string `json:"plan,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	if s.explain == nil {
		writeJSON(w, http.StatusServiceUnavailable, &explainResponse{
			Error: "EXPLAIN is not configured (set DATABASE_URL)",
		})
		return
	}

	var req explainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, &explainResponse{
			Error: "invalid request body: " + err.Error(),
		})
		return
	}

	mode := explain.Explain
	if req.Analyze {
		mode = explain.Analyze
	}

	result, err := s.explain.Run(r.Context(), mode, req.Query, req.Args)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, &explainResponse{
			Error: err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, &explainResponse{Plan: result.Plan})
}

func writeJSON(w http.ResponseWriter, status int, v *explainResponse) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
}
