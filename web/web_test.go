package web_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mickamy/sql-tap/broker"
	"github.com/mickamy/sql-tap/proxy"
	"github.com/mickamy/sql-tap/web"
)

func TestStaticFiles(t *testing.T) {
	t.Parallel()

	srv := web.New(broker.New(8), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("got Content-Type %q, want text/html", ct)
	}
}

func TestSSE_ReceivesEvents(t *testing.T) {
	t.Parallel()

	b := broker.New(8)
	srv := web.New(b, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("got Content-Type %q, want text/event-stream", ct)
	}

	// Wait for subscription to be registered.
	time.Sleep(50 * time.Millisecond)

	b.Publish(proxy.Event{
		ID:        "test-1",
		Op:        proxy.OpQuery,
		Query:     "SELECT 1",
		StartTime: time.Date(2026, 2, 20, 15, 4, 5, 0, time.UTC),
		Duration:  5 * time.Millisecond,
	})

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var ev struct {
			ID         string  `json:"id"`
			Op         string  `json:"op"`
			Query      string  `json:"query"`
			DurationMs float64 `json:"duration_ms"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ev.ID != "test-1" {
			t.Fatalf("got ID %q, want test-1", ev.ID)
		}
		if ev.Op != "Query" {
			t.Fatalf("got Op %q, want Query", ev.Op)
		}
		if ev.Query != "SELECT 1" {
			t.Fatalf("got Query %q, want SELECT 1", ev.Query)
		}
		if ev.DurationMs < 4.9 || ev.DurationMs > 5.1 {
			t.Fatalf("got DurationMs %f, want ~5.0", ev.DurationMs)
		}
		return // success
	}
	t.Fatal("no SSE data received")
}

func TestSSE_DisconnectUnsubscribes(t *testing.T) {
	t.Parallel()

	b := broker.New(8)
	srv := web.New(b, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	if n := b.SubscriberCount(); n != 1 {
		t.Fatalf("got %d subscribers, want 1", n)
	}

	cancel()
	_ = resp.Body.Close()

	// Wait for cleanup.
	time.Sleep(100 * time.Millisecond)
	if n := b.SubscriberCount(); n != 0 {
		t.Fatalf("got %d subscribers after disconnect, want 0", n)
	}
}

func TestExplain_NotConfigured(t *testing.T) {
	t.Parallel()

	srv := web.New(broker.New(8), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := strings.NewReader(`{"query":"SELECT 1","args":[],"analyze":false}`)
	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/explain", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("got status %d, want 503", resp.StatusCode)
	}

	var result struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Error, "not configured") {
		t.Fatalf("got error %q, want contains 'not configured'", result.Error)
	}
}
