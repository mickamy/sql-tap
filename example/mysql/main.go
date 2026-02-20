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

	_ "github.com/go-sql-driver/mysql"
)

const defaultDSN = "mysql:mysql@tcp(localhost:3307)/db?parseTime=true"

const upsertUser = "INSERT INTO users (name, email) VALUES (?, ?)" +
	" ON DUPLICATE KEY UPDATE name = VALUES(name)"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func getDSN() string {
	if v := os.Getenv("DATABASE_DSN"); v != "" {
		return v
	}
	return defaultDSN
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dsn := getDSN()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	fmt.Printf("connected to mysql via %s\n", dsn)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for i := 1; ; i++ {
		doQueries(ctx, db, i)
		doTransaction(ctx, db, i)
		doRollback(ctx, db, i)
		doConcurrentTransactions(ctx, db, i)
		doLongQuery(ctx, db, i)

		if i%3 == 0 {
			doNPlus1(ctx, db, i)
		}

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

	_, err = tx.ExecContext(ctx, "UPDATE users SET name = ? WHERE name = ?", name+"-updated", name)
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

func doNPlus1(ctx context.Context, db *sql.DB, i int) {
	for j := range 10 {
		var name string
		_ = db.QueryRowContext(ctx,
			"SELECT name FROM users WHERE id = ?",
			(i+j)%100+1,
		).Scan(&name)
	}
	fmt.Printf("[%d] N+1 simulation done (10 individual SELECTs)\n", i)
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
				WHEN u1.created_at > NOW() - INTERVAL 1 HOUR THEN 'recent'
				WHEN u1.created_at > NOW() - INTERVAL 1 DAY THEN 'today'
				WHEN u1.created_at > NOW() - INTERVAL 7 DAY THEN 'this_week'
				ELSE 'older'
			END AS freshness,
			CHAR_LENGTH(u1.name) AS name_length,
			UPPER(u1.name) AS upper_name,
			LOWER(u2.name) AS lower_other_name,
			COALESCE(u2.name, 'unknown') AS safe_other_name
		FROM users u1
		CROSS JOIN users u2
		WHERE u1.id != u2.id
			AND u1.name LIKE CONCAT('%', ?, '%')
			AND u2.created_at > NOW() - INTERVAL 1 DAY
		ORDER BY u1.created_at DESC, u2.name ASC
		LIMIT 1
	`, fmt.Sprintf("user-%d", i)).Scan(&dummy)
}
