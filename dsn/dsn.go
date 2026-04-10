package dsn

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// DetectDriver infers the database driver name from a DSN string.
//
//   - "postgres://" or "postgresql://" prefix -> "pgx"
//   - Contains "@" (MySQL-style user:pass@tcp(...)/db) -> "mysql"
//   - Contains "=" but not "@" (PostgreSQL key=value style) -> "pgx"
//   - Otherwise -> error
func DetectDriver(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("dsn: empty DSN")
	}

	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		return "pgx", nil
	case strings.Contains(raw, "@"):
		return "mysql", nil
	case strings.Contains(raw, "="):
		return "pgx", nil
	}

	return "", fmt.Errorf("dsn: cannot detect driver from: %s", raw)
}

// Open detects the driver from the DSN and opens a *sql.DB.
func Open(raw string) (*sql.DB, error) {
	driver, err := DetectDriver(raw)
	if err != nil {
		return nil, err
	}

	openDSN := raw
	if driver == "mysql" {
		openDSN = strings.TrimPrefix(openDSN, "mysql://")
		openDSN = mysqlURLToDriverDSN(openDSN)
	}

	db, err := sql.Open(driver, openDSN)
	if err != nil {
		return nil, fmt.Errorf("dsn: open: %w", err)
	}
	return db, nil
}

// mysqlURLToDriverDSN converts a MySQL URL-style DSN to go-sql-driver format when needed.
// The go-sql-driver requires TCP addresses to be wrapped as tcp(host:port).
// DSNs that already use a protocol wrapper (e.g. tcp(...) or unix(...)) are returned unchanged.
//
// Example:
//
//	"user:pass@host:3306/db"          →  "user:pass@tcp(host:3306)/db"
//	"user:pass@tcp(host:3306)/db"     →  "user:pass@tcp(host:3306)/db"  (unchanged)
//	"user:pass@unix(/tmp/sock)/db"    →  "user:pass@unix(/tmp/sock)/db" (unchanged)
func mysqlURLToDriverDSN(dsn string) string {
	// Find the last @ to separate user:pass from the address part.
	// Using LastIndex mirrors how go-sql-driver itself locates the credential boundary,
	// which correctly handles passwords that contain @.
	atIdx := strings.LastIndex(dsn, "@")
	if atIdx < 0 {
		return dsn
	}

	addrPart := dsn[atIdx+1:]

	// Locate the first slash, which separates the network address from the database name.
	slashIdx := strings.Index(addrPart, "/")
	if slashIdx < 0 {
		return dsn
	}

	// If there is an opening parenthesis before the first slash, the address already
	// has a protocol wrapper such as tcp(...) or unix(...).
	parenIdx := strings.Index(addrPart, "(")
	if parenIdx >= 0 && parenIdx < slashIdx {
		return dsn
	}

	// The host portion is a bare "host:port" – wrap it with tcp().
	host := addrPart[:slashIdx]
	if strings.Contains(host, ":") {
		return dsn[:atIdx+1] + "tcp(" + host + ")" + addrPart[slashIdx:]
	}

	return dsn
}
