package ci_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/mickamy/sql-tap/broker"
	"github.com/mickamy/sql-tap/ci"
	tapv1 "github.com/mickamy/sql-tap/gen/tap/v1"
	"github.com/mickamy/sql-tap/proxy"
	"github.com/mickamy/sql-tap/server"
)

func TestResult_HasProblems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r    ci.Result
		want bool
	}{
		{
			name: "no problems",
			r:    ci.Result{TotalQueries: 10},
			want: false,
		},
		{
			name: "with problems",
			r: ci.Result{
				TotalQueries: 10,
				Problems: []ci.Problem{
					{Kind: ci.ProblemNPlus1, Query: "SELECT 1", Count: 5},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.r.HasProblems(); got != tt.want {
				t.Errorf("HasProblems() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResult_Report_NoProblems(t *testing.T) {
	t.Parallel()

	r := ci.Result{TotalQueries: 42}
	report := r.Report()

	assertContains(t, report, "Captured: 42 queries")
	assertContains(t, report, "No problems found.")
}

func TestResult_Report_WithProblems(t *testing.T) {
	t.Parallel()

	r := ci.Result{
		TotalQueries: 100,
		Problems: []ci.Problem{
			{Kind: ci.ProblemNPlus1, Query: "SELECT * FROM comments WHERE post_id = ?", Count: 12},
			{Kind: ci.ProblemSlowQuery, Query: "SELECT * FROM users JOIN ...", Count: 3, AvgDuration: 523 * time.Millisecond},
		},
	}
	report := r.Report()

	assertContains(t, report, "Captured: 100 queries")
	assertContains(t, report, "[N+1]")
	assertContains(t, report, "detected 12 times")
	assertContains(t, report, "[SLOW]")
	assertContains(t, report, "avg 523ms")
	assertContains(t, report, "2 problems found")
}

func TestAggregate(t *testing.T) {
	t.Parallel()

	events := []*tapv1.QueryEvent{
		{Query: "SELECT 1", NormalizedQuery: "SELECT ?"},
		{Query: "SELECT * FROM users WHERE id = 1", NormalizedQuery: "SELECT * FROM users WHERE id = ?", NPlus_1: true},
		{Query: "SELECT * FROM users WHERE id = 2", NormalizedQuery: "SELECT * FROM users WHERE id = ?", NPlus_1: true},
		{Query: "SELECT * FROM users WHERE id = 3", NormalizedQuery: "SELECT * FROM users WHERE id = ?", NPlus_1: true},
		{
			Query:           "SELECT * FROM posts JOIN comments ON ...",
			NormalizedQuery: "SELECT * FROM posts JOIN comments ON ...",
			SlowQuery:       true,
			Duration:        durationpb.New(200 * time.Millisecond),
		},
		{
			Query:           "SELECT * FROM posts JOIN comments ON ...",
			NormalizedQuery: "SELECT * FROM posts JOIN comments ON ...",
			SlowQuery:       true,
			Duration:        durationpb.New(400 * time.Millisecond),
		},
	}

	result := ci.Aggregate(events)

	if result.TotalQueries != 6 {
		t.Errorf("TotalQueries = %d, want 6", result.TotalQueries)
	}
	if len(result.Problems) != 2 {
		t.Fatalf("len(Problems) = %d, want 2", len(result.Problems))
	}

	// N+1 problems are sorted first (N+1 < SLOW lexically).
	nplus1 := result.Problems[0]
	if nplus1.Kind != ci.ProblemNPlus1 {
		t.Errorf("Problems[0].Kind = %s, want N+1", nplus1.Kind)
	}
	if nplus1.Count != 3 {
		t.Errorf("Problems[0].Count = %d, want 3", nplus1.Count)
	}

	slow := result.Problems[1]
	if slow.Kind != ci.ProblemSlowQuery {
		t.Errorf("Problems[1].Kind = %s, want SLOW", slow.Kind)
	}
	if slow.Count != 2 {
		t.Errorf("Problems[1].Count = %d, want 2", slow.Count)
	}
	if slow.AvgDuration != 300*time.Millisecond {
		t.Errorf("Problems[1].AvgDuration = %s, want 300ms", slow.AvgDuration)
	}
}

func TestAggregate_NoProblemEvents(t *testing.T) {
	t.Parallel()

	events := []*tapv1.QueryEvent{
		{Query: "SELECT 1"},
		{Query: "INSERT INTO users VALUES (1)"},
	}

	result := ci.Aggregate(events)

	if result.TotalQueries != 2 {
		t.Errorf("TotalQueries = %d, want 2", result.TotalQueries)
	}
	if result.HasProblems() {
		t.Error("expected no problems")
	}
}

func TestAggregate_Empty(t *testing.T) {
	t.Parallel()

	result := ci.Aggregate(nil)

	if result.TotalQueries != 0 {
		t.Errorf("TotalQueries = %d, want 0", result.TotalQueries)
	}
	if result.HasProblems() {
		t.Error("expected no problems")
	}
}

func TestAggregate_BothNPlus1AndSlow(t *testing.T) {
	t.Parallel()

	events := []*tapv1.QueryEvent{
		{
			Query:           "SELECT * FROM users WHERE id = ?",
			NormalizedQuery: "SELECT * FROM users WHERE id = ?",
			NPlus_1:         true,
			SlowQuery:       true,
			Duration:        durationpb.New(150 * time.Millisecond),
		},
	}

	result := ci.Aggregate(events)

	if len(result.Problems) != 2 {
		t.Fatalf("len(Problems) = %d, want 2 (one N+1, one SLOW)", len(result.Problems))
	}
}

func TestAggregate_UsesRawQueryWhenNormalizedEmpty(t *testing.T) {
	t.Parallel()

	const rawQuery = "SELECT id, name FROM users"
	events := []*tapv1.QueryEvent{
		{Query: rawQuery, NPlus_1: true},
		{Query: rawQuery, NPlus_1: true},
	}

	result := ci.Aggregate(events)

	if len(result.Problems) != 1 {
		t.Fatalf("len(Problems) = %d, want 1", len(result.Problems))
	}
	if result.Problems[0].Query != rawQuery {
		t.Errorf("Query = %q, want %q", result.Problems[0].Query, rawQuery)
	}
}

func startServer(t *testing.T, b *broker.Broker) string {
	t.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := server.New(b, nil)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	return lis.Addr().String()
}

func TestRun_AggregatesEventsOnContextCancel(t *testing.T) {
	t.Parallel()

	b := broker.New(8)
	addr := startServer(t, b)

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan ci.Result, 1)
	errc := make(chan error, 1)
	go func() {
		result, err := ci.Run(ctx, addr)
		if err != nil {
			errc <- err
			return
		}
		done <- result
	}()

	// Wait for subscription to register.
	time.Sleep(50 * time.Millisecond)

	b.Publish(proxy.Event{ID: "1", Op: proxy.OpQuery, Query: "SELECT 1"})
	b.Publish(proxy.Event{
		ID: "2", Op: proxy.OpQuery, Query: "SELECT id FROM users WHERE id = 1",
		NPlus1:          true,
		NormalizedQuery: "SELECT id FROM users WHERE id = ?",
	})
	b.Publish(proxy.Event{
		ID: "3", Op: proxy.OpQuery, Query: "SELECT id FROM users WHERE id = 2",
		NPlus1:          true,
		NormalizedQuery: "SELECT id FROM users WHERE id = ?",
	})

	// Give events time to arrive, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case result := <-done:
		if result.TotalQueries != 3 {
			t.Errorf("TotalQueries = %d, want 3", result.TotalQueries)
		}
		if !result.HasProblems() {
			t.Error("expected problems")
		}
	case err := <-errc:
		t.Fatalf("Run returned error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}
}

func TestRun_NoProblemEvents(t *testing.T) {
	t.Parallel()

	b := broker.New(8)
	addr := startServer(t, b)

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan ci.Result, 1)
	errc := make(chan error, 1)
	go func() {
		result, err := ci.Run(ctx, addr)
		if err != nil {
			errc <- err
			return
		}
		done <- result
	}()

	time.Sleep(50 * time.Millisecond)

	b.Publish(proxy.Event{ID: "1", Op: proxy.OpQuery, Query: "SELECT 1"})
	b.Publish(proxy.Event{ID: "2", Op: proxy.OpExec, Query: "INSERT INTO t VALUES (1)"})

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case result := <-done:
		if result.TotalQueries != 2 {
			t.Errorf("TotalQueries = %d, want 2", result.TotalQueries)
		}
		if result.HasProblems() {
			t.Error("expected no problems")
		}
	case err := <-errc:
		t.Fatalf("Run returned error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !contains(s, substr) {
		t.Errorf("expected report to contain %q, got:\n%s", substr, s)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
