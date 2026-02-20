package detect_test

import (
	"testing"
	"time"

	"github.com/mickamy/sql-tap/detect"
)

func TestBelowThreshold(t *testing.T) {
	t.Parallel()
	d := detect.New(5, time.Second, 10*time.Second)
	now := time.Now()
	q := "SELECT id, name FROM users WHERE id = $1"

	for i := range 4 {
		r := d.Record(q, now.Add(time.Duration(i)*100*time.Millisecond))
		if r.Matched {
			t.Fatal("unexpected match before threshold")
		}
		if r.Alert != nil {
			t.Fatal("unexpected alert before threshold")
		}
	}
}

func TestAtThreshold(t *testing.T) {
	t.Parallel()
	d := detect.New(5, time.Second, 10*time.Second)
	now := time.Now()
	q := "SELECT id, name FROM users WHERE id = $1"

	for i := range 4 {
		d.Record(q, now.Add(time.Duration(i)*100*time.Millisecond))
	}

	r := d.Record(q, now.Add(400*time.Millisecond))
	if !r.Matched {
		t.Fatal("expected matched at threshold")
	}
	if r.Alert == nil {
		t.Fatal("expected alert at threshold")
	}
	if r.Alert.Count != 5 {
		t.Fatalf("got count %d, want 5", r.Alert.Count)
	}
	if r.Alert.Query != q {
		t.Fatalf("got query %q, want %q", r.Alert.Query, q)
	}
}

func TestMatchedAfterThreshold(t *testing.T) {
	t.Parallel()
	d := detect.New(5, time.Second, 10*time.Second)
	now := time.Now()
	q := "SELECT id, name FROM users WHERE id = $1"

	// Cross threshold.
	for i := range 5 {
		d.Record(q, now.Add(time.Duration(i)*100*time.Millisecond))
	}

	// Subsequent events within window should be matched but no alert (cooldown).
	for i := range 5 {
		r := d.Record(q, now.Add(time.Duration(500+i*100)*time.Millisecond))
		if !r.Matched {
			t.Fatalf("event %d: expected matched after threshold", i)
		}
		if r.Alert != nil {
			t.Fatalf("event %d: expected cooldown to suppress alert", i)
		}
	}
}

func TestWindowExpiry(t *testing.T) {
	t.Parallel()
	d := detect.New(5, time.Second, 10*time.Second)
	now := time.Now()
	q := "SELECT id, name FROM users WHERE id = $1"

	// 3 queries in first batch.
	for i := range 3 {
		d.Record(q, now.Add(time.Duration(i)*100*time.Millisecond))
	}

	// 3 queries after window expires. Total 6, but only 3 in window.
	after := now.Add(2 * time.Second)
	for i := range 3 {
		r := d.Record(q, after.Add(time.Duration(i)*100*time.Millisecond))
		if r.Matched {
			t.Fatal("unexpected match: only 3 in window")
		}
	}
}

func TestCooldownExpiry(t *testing.T) {
	t.Parallel()
	d := detect.New(5, 2*time.Second, time.Second)
	now := time.Now()
	q := "SELECT id, name FROM users WHERE id = $1"

	// Trigger first alert.
	for i := range 5 {
		d.Record(q, now.Add(time.Duration(i)*100*time.Millisecond))
	}

	// After cooldown expires, should alert again.
	after := now.Add(1500 * time.Millisecond)
	r := d.Record(q, after)
	if !r.Matched {
		t.Fatal("expected matched after cooldown expired")
	}
	if r.Alert == nil {
		t.Fatal("expected alert after cooldown expired")
	}
}

func TestDifferentTemplates(t *testing.T) {
	t.Parallel()
	d := detect.New(3, time.Second, 10*time.Second)
	now := time.Now()
	q1 := "SELECT id, name FROM users WHERE id = $1"
	q2 := "SELECT id, title FROM posts WHERE user_id = $1"

	// Interleave: 2 of each, below threshold for both.
	d.Record(q1, now)
	d.Record(q2, now.Add(100*time.Millisecond))
	d.Record(q1, now.Add(200*time.Millisecond))
	d.Record(q2, now.Add(300*time.Millisecond))

	// q1 hits threshold.
	r := d.Record(q1, now.Add(400*time.Millisecond))
	if r.Alert == nil {
		t.Fatal("expected alert for q1")
	}
	if r.Alert.Query != q1 {
		t.Fatalf("got query %q, want %q", r.Alert.Query, q1)
	}

	// q2 also hits threshold (3 occurrences).
	r = d.Record(q2, now.Add(500*time.Millisecond))
	if r.Alert == nil {
		t.Fatal("expected alert for q2")
	}
	if r.Alert.Query != q2 {
		t.Fatalf("got query %q, want %q", r.Alert.Query, q2)
	}
}

func TestEmptyQuery(t *testing.T) {
	t.Parallel()
	d := detect.New(1, time.Second, 10*time.Second)
	r := d.Record("", time.Now())
	if r.Matched {
		t.Fatal("expected no match for empty query")
	}
}
