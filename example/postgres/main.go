package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const dsn = "postgres://postgres:postgres@localhost:5433/db?sslmode=disable"

const upsertUser = "INSERT INTO users (name, email) VALUES ($1, $2)" +
	" ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	fmt.Println("connected to postgres via tapd proxy on :5433")

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for i := 1; ; i++ {
		doQueries(ctx, db, i)
		doTransaction(ctx, db, i)
		doRollback(ctx, db, i)
		doConcurrentTransactions(ctx, db, i)
		doLongQuery(ctx, db, i)
		doUUIDQuery(ctx, db)

		select {
		case <-ctx.Done():
			fmt.Println("shutting down")
			return nil
		case <-ticker.C:
		}
	}
}

func doQueries(ctx context.Context, db *sql.DB, i int) {
	name := fmt.Sprintf("user-%d", i)

	_, err := db.ExecContext(ctx, upsertUser, name, name+"@example.com")
	if err != nil {
		log.Printf("insert: %v", err)
		return
	}

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		log.Printf("count: %v", err)
		return
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("begin: %v", err)
		return
	}

	_, err = tx.ExecContext(ctx, "UPDATE users SET name = $1 WHERE name = $2", name+"-updated", name)
	if err != nil {
		_ = tx.Rollback()
		log.Printf("update: %v", err)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("commit: %v", err)
		return
	}

	fmt.Printf("[%d] inserted + updated %s (total: %d)\n", i, name, count)
}

func doTransaction(ctx context.Context, db *sql.DB, i int) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("tx begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	name := fmt.Sprintf("tx-user-%d", i)
	email := name + "@example.com"
	if _, err := tx.ExecContext(ctx, upsertUser, name, email); err != nil {
		log.Printf("tx insert: %v", err)
		return
	}

	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		log.Printf("tx count: %v", err)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("tx commit: %v", err)
		return
	}

	fmt.Printf("[%d] tx committed %s (total: %d)\n", i, name, count)
}

func doRollback(ctx context.Context, db *sql.DB, i int) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("rollback begin: %v", err)
		return
	}

	name := fmt.Sprintf("rollback-user-%d", i)
	email := name + "@example.com"
	if _, err := tx.ExecContext(ctx, upsertUser, name, email); err != nil {
		log.Printf("rollback insert: %v", err)
		_ = tx.Rollback()
		return
	}

	if err := tx.Rollback(); err != nil {
		log.Printf("rollback: %v", err)
		return
	}

	fmt.Printf("[%d] rolled back %s\n", i, name)
}

func doConcurrentTransactions(ctx context.Context, db *sql.DB, i int) {
	var wg sync.WaitGroup
	for g := range 3 {
		wg.Go(func() {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return
			}
			defer func() { _ = tx.Rollback() }()

			name := fmt.Sprintf("concurrent-%d-%d", i, g)
			email := name + "@example.com"
			_, _ = tx.ExecContext(ctx, upsertUser, name, email)
			_ = tx.Commit()
		})
	}
	wg.Wait()
}

func doUUIDQuery(ctx context.Context, _ *sql.DB) {
	// Use pgx native connection to send UUID params in binary format.
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Printf("uuid pgx connect: %v", err)
		return
	}
	defer func() { _ = conn.Close(ctx) }()

	ids := [][16]byte{
		{0xa0, 0xee, 0xbc, 0x99, 0x9c, 0x0b, 0x4e, 0xf8, 0xbb, 0x6d, 0x6b, 0xb9, 0xbd, 0x38, 0x0a, 0x11},
		{0xb1, 0xff, 0xc9, 0x9a, 0x8d, 0x1c, 0x4e, 0xf9, 0xcc, 0x7e, 0x7c, 0xca, 0xde, 0x49, 0x1b, 0x22},
		{0xc2, 0x00, 0xda, 0x9b, 0x7e, 0x2d, 0x4f, 0xfa, 0xdd, 0x8f, 0x8d, 0xdb, 0xef, 0x5a, 0x2c, 0x33},
	}
	var name string
	for _, id := range ids {
		_ = conn.QueryRow(ctx,
			"SELECT name FROM projects WHERE id = $1", id,
		).Scan(&name)
	}
	fmt.Printf("uuid query done (last: %s)\n", name)
}

func doLongQuery(ctx context.Context, db *sql.DB, i int) {
	var dummy int
	_ = db.QueryRowContext(ctx, `
		SELECT
			u1.id,
			u1.name,
			u1.created_at,
			u2.id AS other_id,
			u2.name AS other_name,
			u2.created_at AS other_created_at,
			COUNT(*) OVER () AS total_count,
			ROW_NUMBER() OVER (ORDER BY u1.created_at DESC) AS row_num,
			CASE
				WHEN u1.created_at > NOW() - INTERVAL '1 hour' THEN 'recent'
				WHEN u1.created_at > NOW() - INTERVAL '1 day' THEN 'today'
				WHEN u1.created_at > NOW() - INTERVAL '7 days' THEN 'this_week'
				ELSE 'older'
			END AS freshness,
			LENGTH(u1.name) AS name_length,
			UPPER(u1.name) AS upper_name,
			LOWER(u2.name) AS lower_other_name,
			COALESCE(u2.name, 'unknown') AS safe_other_name
		FROM users u1
		CROSS JOIN users u2
		WHERE u1.id != u2.id
			AND u1.name LIKE $1
			AND u2.created_at > NOW() - INTERVAL '1 day'
		ORDER BY u1.created_at DESC, u2.name ASC
		LIMIT 1
	`, fmt.Sprintf("%%user-%d%%", i)).Scan(&dummy)
}
