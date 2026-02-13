package explain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DetectDriver infers the database driver name from a DSN string.
//
//   - "postgres://" or "postgresql://" prefix -> "pgx"
//   - Contains "@" (MySQL-style user:pass@tcp(...)/db) -> "mysql"
//   - Contains "=" but not "@" (PostgreSQL key=value style) -> "pgx"
//   - Otherwise -> error
func DetectDriver(dsn string) (string, error) {
	if dsn == "" {
		return "", errors.New("empty DSN")
	}

	lower := strings.ToLower(dsn)
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		return "pgx", nil
	case strings.Contains(dsn, "@"):
		return "mysql", nil
	case strings.Contains(dsn, "="):
		return "pgx", nil
	}

	return "", fmt.Errorf("cannot detect driver from DSN: %s", dsn)
}

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

func (m Mode) prefix() string {
	switch m {
	case Explain:
		return "EXPLAIN "
	case Analyze:
		return "EXPLAIN ANALYZE "
	}
	return "EXPLAIN "
}

// Result holds the output of an EXPLAIN query.
type Result struct {
	Plan     string
	Duration time.Duration
}

// Client wraps a database connection for running EXPLAIN queries.
type Client struct {
	db *sql.DB
}

// NewClient creates a new Client from an existing *sql.DB.
func NewClient(db *sql.DB) *Client {
	return &Client{db: db}
}

// Run executes EXPLAIN or EXPLAIN ANALYZE for the given query with optional args.
func (c *Client) Run(ctx context.Context, mode Mode, query string, args []string) (*Result, error) {
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}

	start := time.Now()
	rows, err := c.db.QueryContext(ctx, mode.prefix()+query, anyArgs...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	return &Result{
		Plan:     strings.Join(lines, "\n"),
		Duration: time.Since(start),
	}, nil
}

// Close closes the underlying database connection.
func (c *Client) Close() error {
	if err := c.db.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}
