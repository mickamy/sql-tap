package explain

import (
	"context"
	"database/sql"
	"fmt"
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
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}

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

// Close closes the underlying database connection.
func (c *Client) Close() error {
	if err := c.db.Close(); err != nil {
		return fmt.Errorf("explain: close: %w", err)
	}
	return nil
}
