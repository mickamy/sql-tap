package detect

import (
	"sync"
	"time"
)

// Alert represents a detected N+1 query pattern.
type Alert struct {
	Query string
	Count int
}

// Detector tracks query frequency and detects N+1 patterns.
type Detector struct {
	mu        sync.Mutex
	threshold int
	window    time.Duration
	cooldown  time.Duration
	queries   map[string][]time.Time
	lastAlert map[string]time.Time
}

// New creates a Detector.
// threshold: number of occurrences to trigger (e.g., 5).
// window: time window to count within (e.g., 1s).
// cooldown: minimum time between alerts for the same template (e.g., 10s).
func New(threshold int, window, cooldown time.Duration) *Detector {
	return &Detector{
		threshold: threshold,
		window:    window,
		cooldown:  cooldown,
		queries:   make(map[string][]time.Time),
		lastAlert: make(map[string]time.Time),
	}
}

// Result holds the outcome of a Record call.
type Result struct {
	// Matched is true when the query count is at or above the threshold
	// within the time window. Use this to mark every event in the pattern.
	Matched bool
	// Alert is non-nil only when the threshold is first crossed (respecting
	// cooldown). Use this to trigger a one-time notification.
	Alert *Alert
}

// Record registers a query occurrence and returns a Result.
func (d *Detector) Record(query string, t time.Time) Result {
	if query == "" {
		return Result{}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	cutoff := t.Add(-d.window)

	// Evict old entries and append new timestamp.
	times := d.queries[query]
	start := 0
	for start < len(times) && times[start].Before(cutoff) {
		start++
	}
	times = append(times[start:], t)
	d.queries[query] = times

	if len(times) < d.threshold {
		return Result{}
	}

	res := Result{Matched: true}

	// Only fire alert notification respecting cooldown.
	if last, ok := d.lastAlert[query]; !ok || t.Sub(last) >= d.cooldown {
		d.lastAlert[query] = t
		res.Alert = &Alert{Query: query, Count: len(times)}
	}

	return res
}
