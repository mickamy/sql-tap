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
	"google.golang.org/grpc/codes"
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
		default:
			fmt.Fprintf(&b, "  [%s] %s  (%d occurrences)\n", string(p.Kind), p.Query, p.Count)
		}
	}

	fmt.Fprintf(&b, "\nExit: 1 (%d problems found)\n", len(r.Problems))
	return b.String()
}

// Run connects to the gRPC server at addr, collects query events until ctx is
// cancelled or the server closes the stream, and returns the aggregated result.
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
	a := newAggregator()

	for {
		resp, err := stream.Recv()
		if err != nil {
			if isStreamDone(ctx, err) {
				break
			}
			return Result{}, fmt.Errorf("recv: %w", err)
		}
		a.add(resp.GetEvent())
	}

	return a.result(), nil
}

func isStreamDone(ctx context.Context, err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	if ctx.Err() != nil {
		return true
	}
	code := status.Code(err)
	return code == codes.Canceled || code == codes.DeadlineExceeded
}

type queryStats struct {
	nplus1Count int
	slowCount   int
	totalDur    time.Duration
}

type aggregator struct {
	total   int
	grouped map[string]*queryStats
}

func newAggregator() *aggregator {
	return &aggregator{grouped: make(map[string]*queryStats)}
}

func (a *aggregator) add(e *tapv1.QueryEvent) {
	a.total++
	if !e.GetNPlus_1() && !e.GetSlowQuery() {
		return
	}
	q := normalizedOrRaw(e)
	s, ok := a.grouped[q]
	if !ok {
		s = &queryStats{}
		a.grouped[q] = s
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

func (a *aggregator) result() Result {
	r := Result{TotalQueries: a.total}

	for q, s := range a.grouped {
		if s.nplus1Count > 0 {
			r.Problems = append(r.Problems, Problem{
				Kind:  ProblemNPlus1,
				Query: q,
				Count: s.nplus1Count,
			})
		}
		if s.slowCount > 0 {
			avg := s.totalDur / time.Duration(s.slowCount)
			r.Problems = append(r.Problems, Problem{
				Kind:        ProblemSlowQuery,
				Query:       q,
				Count:       s.slowCount,
				AvgDuration: avg,
			})
		}
	}

	sort.Slice(r.Problems, func(i, j int) bool {
		if r.Problems[i].Kind != r.Problems[j].Kind {
			return r.Problems[i].Kind < r.Problems[j].Kind
		}
		return r.Problems[i].Count > r.Problems[j].Count
	})

	return r
}

// Aggregate computes the CI result from the given events.
// Intended for testing; the streaming path uses aggregator directly.
func Aggregate(events []*tapv1.QueryEvent) Result {
	a := newAggregator()
	for _, e := range events {
		a.add(e)
	}
	return a.result()
}

func normalizedOrRaw(e *tapv1.QueryEvent) string {
	if nq := e.GetNormalizedQuery(); nq != "" {
		return nq
	}
	return e.GetQuery()
}
