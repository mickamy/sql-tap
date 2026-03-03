package explain

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Mode selects between EXPLAIN and EXPLAIN ANALYZE.
type Mode int

const (
	Explain Mode = iota // EXPLAIN (plan only)
	Analyze             // EXPLAIN ANALYZE (plan + actual execution)
)

func (m Mode) String() string {
	switch m {
	case Explain:
		return "EXPLAIN"
	case Analyze:
		return "EXPLAIN ANALYZE"
	}
	return "EXPLAIN"
}

func (m Mode) prefix(driver Driver) string {
	switch driver {
	case MySQL:
		switch m {
		case Explain:
			return "EXPLAIN FORMAT=TREE "
		case Analyze:
			return "EXPLAIN ANALYZE "
		}
	case Postgres, TiDB:
		switch m {
		case Explain:
			return "EXPLAIN "
		case Analyze:
			return "EXPLAIN ANALYZE "
		}
	}
	return "EXPLAIN "
}

// Result holds the output of an EXPLAIN query.
type Result struct {
	Plan     string
	Duration time.Duration
}

// Driver identifies the database driver for EXPLAIN syntax differences.
type Driver int

const (
	Postgres Driver = iota
	MySQL
	TiDB
)

// Client wraps a database connection for running EXPLAIN queries.
type Client struct {
	db     *sql.DB
	driver Driver
}

// NewClient creates a new Client from an existing *sql.DB.
func NewClient(db *sql.DB, driver Driver) *Client {
	return &Client{db: db, driver: driver}
}

// Run executes EXPLAIN or EXPLAIN ANALYZE for the given query with optional args.
func (c *Client) Run(ctx context.Context, mode Mode, query string, args []string) (*Result, error) {
	anyArgs := buildAnyArgs(query, args)

	// MySQL/TiDB cannot parse placeholder ? without args; replace with NULL for plan-only EXPLAIN.
	q := query
	if (c.driver == MySQL || c.driver == TiDB) && len(anyArgs) == 0 {
		q = strings.ReplaceAll(q, "?", "NULL")
	}

	start := time.Now()
	rows, err := c.db.QueryContext(ctx, mode.prefix(c.driver)+q, anyArgs...)
	if err != nil {
		return nil, fmt.Errorf("explain: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("explain: columns: %w", err)
	}

	var lines []string
	if len(cols) > 1 {
		lines = append(lines, strings.Join(cols, "\t"))
	}
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("explain: scan: %w", err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			parts[i] = v.String
		}
		lines = append(lines, strings.Join(parts, "\t"))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("explain: rows: %w", err)
	}

	return &Result{
		Plan:     strings.Join(lines, "\n"),
		Duration: time.Since(start),
	}, nil
}

// timestampCastRe matches PostgreSQL-style timestamp cast placeholders such as
// $1::TIMESTAMP, $2::TIMESTAMPTZ, $3::TIMESTAMP WITH TIME ZONE, etc.
// The prefix "TIMESTAMP" covers all variants because TIMESTAMPTZ and
// "TIMESTAMP WITH/WITHOUT TIME ZONE" all begin with that substring.
var timestampCastRe = regexp.MustCompile(`(?i)\$(\d+)\s*::\s*TIMESTAMP`)

// pgEpochUnix is the Unix timestamp of PostgreSQL's internal epoch (2000-01-01 00:00:00 UTC).
const pgEpochUnix int64 = 946684800

// buildAnyArgs converts string args to []any for use in QueryContext.
// For args whose corresponding query placeholder is cast to a timestamp type
// (e.g. $2::TIMESTAMP WITH TIME ZONE), it tries to interpret the value as a
// PostgreSQL binary-encoded timestamp (int64 microseconds since 2000-01-01 UTC)
// and converts it to time.Time. This prevents the "date/time field value out of
// range" error that occurs when a captured binary timestamp is re-used as a plain
// string in a parameterized EXPLAIN query.
func buildAnyArgs(query string, args []string) []any {
	tsParams := parseTimestampParams(query)
	anyArgs := make([]any, len(args))
	for i, a := range args {
		if tsParams[i+1] {
			if t, ok := parsePGTimestamp(a); ok {
				anyArgs[i] = t
				continue
			}
		}
		anyArgs[i] = a
	}
	return anyArgs
}

// parseTimestampParams returns the set of 1-indexed parameter numbers that are
// cast to a timestamp type in the query.
func parseTimestampParams(query string) map[int]bool {
	m := make(map[int]bool)
	for _, match := range timestampCastRe.FindAllStringSubmatch(query, -1) {
		if n, err := strconv.Atoi(match[1]); err == nil {
			m[n] = true
		}
	}
	return m
}

// parsePGTimestamp attempts to interpret s as a PostgreSQL binary-encoded
// timestamp: an int64 number of microseconds since 2000-01-01 00:00:00 UTC.
// Returns the corresponding time.Time and true on success.
func parsePGTimestamp(s string) (time.Time, bool) {
	microsecs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	sec := microsecs / 1_000_000
	usec := microsecs % 1_000_000
	// Normalize: for negative microsecs, usec is negative; carry into sec.
	if usec < 0 {
		sec--
		usec += 1_000_000
	}
	return time.Unix(sec+pgEpochUnix, usec*1_000).UTC(), true
}

// Close closes the underlying database connection.
func (c *Client) Close() error {
	if err := c.db.Close(); err != nil {
		return fmt.Errorf("explain: close: %w", err)
	}
	return nil
}
