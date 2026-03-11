package ci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	tapv1 "github.com/mickamy/sql-tap/gen/tap/v1"
)

// Result holds the CI run outcome.
type Result struct {
	TotalQueries int
	Problems     []Problem
}

// HasProblems reports whether any issues were detected.
func (r Result) HasProblems() bool {
	return len(r.Problems) > 0
}

// ProblemKind categorizes a detected issue.
type ProblemKind string

const (
	ProblemNPlus1    ProblemKind = "N+1"
	ProblemSlowQuery ProblemKind = "SLOW"
)

// Problem describes a single detected issue.
type Problem struct {
	Kind  ProblemKind
	Query string
	Count int
	// AvgDuration is set only for ProblemSlowQuery.
	AvgDuration time.Duration
}

// Report formats the result as a human-readable string.
func (r Result) Report() string {
	var b strings.Builder
	b.WriteString("sql-tap CI Report\n")
	b.WriteString("=================\n")
	fmt.Fprintf(&b, "Captured: %d queries\n", r.TotalQueries)

	if !r.HasProblems() {
		b.WriteString("\nNo problems found.\n")
		return b.String()
	}

	b.WriteString("\nProblems found:\n")
	for _, p := range r.Problems {
		switch p.Kind {
		case ProblemNPlus1:
			fmt.Fprintf(&b, "  [N+1]  %s  (detected %d times)\n", p.Query, p.Count)
		case ProblemSlowQuery:
			avg := p.AvgDuration.Truncate(time.Millisecond)
			fmt.Fprintf(&b, "  [SLOW] %s  (avg %s, %d occurrences)\n", p.Query, avg, p.Count)
		}
	}

	fmt.Fprintf(&b, "\nExit: 1 (%d problems found)\n", len(r.Problems))
	return b.String()
}

// Run connects to the gRPC server at addr, collects query events until ctx is
// cancelled, and returns the aggregated result.
func Run(ctx context.Context, addr string) (Result, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return Result{}, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	client := tapv1.NewTapServiceClient(conn)
	stream, err := client.Watch(ctx, &tapv1.WatchRequest{})
	if err != nil {
		return Result{}, fmt.Errorf("watch %s: %w", addr, err)
	}

	return collect(ctx, stream)
}

func collect(ctx context.Context, stream tapv1.TapService_WatchClient) (Result, error) {
	var events []*tapv1.QueryEvent

	for {
		resp, err := stream.Recv()
		if err != nil {
			if isStreamDone(ctx, err) {
				break
			}
			return Result{}, fmt.Errorf("recv: %w", err)
		}
		events = append(events, resp.GetEvent())
	}

	return Aggregate(events), nil
}

func isStreamDone(ctx context.Context, err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	if ctx.Err() != nil {
		return true
	}
	// gRPC wraps context errors in status; unwrap and check.
	if s, ok := status.FromError(err); ok {
		msg := s.Message()
		return strings.Contains(msg, "context canceled") ||
			strings.Contains(msg, "context deadline exceeded")
	}
	return false
}

// Aggregate computes the CI result from collected events.
func Aggregate(events []*tapv1.QueryEvent) Result {
	result := Result{TotalQueries: len(events)}

	type stats struct {
		nplus1Count int
		slowCount   int
		totalDur    time.Duration
	}

	grouped := make(map[string]*stats)

	for _, e := range events {
		if !e.GetNPlus_1() && !e.GetSlowQuery() {
			continue
		}
		q := normalizedOrRaw(e)
		s, ok := grouped[q]
		if !ok {
			s = &stats{}
			grouped[q] = s
		}
		if e.GetNPlus_1() {
			s.nplus1Count++
		}
		if e.GetSlowQuery() {
			s.slowCount++
			if d := e.GetDuration(); d != nil {
				s.totalDur += d.AsDuration()
			}
		}
	}

	for q, s := range grouped {
		if s.nplus1Count > 0 {
			result.Problems = append(result.Problems, Problem{
				Kind:  ProblemNPlus1,
				Query: q,
				Count: s.nplus1Count,
			})
		}
		if s.slowCount > 0 {
			avg := s.totalDur / time.Duration(s.slowCount)
			result.Problems = append(result.Problems, Problem{
				Kind:        ProblemSlowQuery,
				Query:       q,
				Count:       s.slowCount,
				AvgDuration: avg,
			})
		}
	}

	sort.Slice(result.Problems, func(i, j int) bool {
		if result.Problems[i].Kind != result.Problems[j].Kind {
			return result.Problems[i].Kind < result.Problems[j].Kind
		}
		return result.Problems[i].Count > result.Problems[j].Count
	})

	return result
}

func normalizedOrRaw(e *tapv1.QueryEvent) string {
	if nq := e.GetNormalizedQuery(); nq != "" {
		return nq
	}
	return e.GetQuery()
}
