package mysql_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go/modules/mysql"

	"github.com/mickamy/sql-tap/proxy"
	proxymysql "github.com/mickamy/sql-tap/proxy/mysql"
)

const (
	testUser     = "root"
	testPassword = "test"
	testDB       = "test"
)

// startMySQL launches a MySQL container and returns its host:port address.
func startMySQL(t *testing.T) string {
	t.Helper()

	ctx := t.Context()
	ctr, err := mysql.Run(ctx, "mysql:8",
		mysql.WithDatabase(testDB),
		mysql.WithUsername(testUser),
		mysql.WithPassword(testPassword),
	)
	if err != nil {
		t.Fatalf("start mysql container: %v", err)
	}
	t.Cleanup(func() {
		if err := ctr.Terminate(context.Background()); err != nil {
			t.Logf("terminate mysql container: %v", err)
		}
	})

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("get host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("get port: %v", err)
	}
	return fmt.Sprintf("%s:%s", host, port.Port())
}

func startProxy(t *testing.T, upstream string) (*proxymysql.Proxy, string) {
	t.Helper()

	// Find an available port.
	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()

	p := proxymysql.New(addr, upstream)
	ctx, cancel := context.WithCancel(t.Context())

	go func() {
		if err := p.ListenAndServe(ctx); err != nil {
			if ctx.Err() == nil {
				t.Logf("proxy error: %v", err)
			}
		}
	}()

	// Wait for proxy to be ready.
	d := net.Dialer{Timeout: 100 * time.Millisecond}
	for range 50 {
		conn, dialErr := d.DialContext(ctx, "tcp", addr)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Cleanup(func() {
		cancel()
		_ = p.Close()
	})

	return p, addr
}

func openDB(t *testing.T, addr string) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?timeout=5s", testUser, testPassword, addr, testDB)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func waitEvent(t *testing.T, ch <-chan proxy.Event) proxy.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event")
		return proxy.Event{}
	}
}

func TestSimpleQuery(t *testing.T) {
	t.Parallel()
	upstream := startMySQL(t)
	p, addr := startProxy(t, upstream)
	db := openDB(t, addr)

	_, err := db.ExecContext(t.Context(), "SELECT 1")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}

	ev := waitEvent(t, p.Events())
	if ev.Query != "SELECT 1" && ev.Query != "select 1" {
		t.Errorf("unexpected query: %q", ev.Query)
	}
	if ev.Error != "" {
		t.Errorf("unexpected error: %q", ev.Error)
	}
}

func TestSelectRows(t *testing.T) {
	t.Parallel()
	upstream := startMySQL(t)
	p, addr := startProxy(t, upstream)
	db := openDB(t, addr)

	rows, err := db.QueryContext(t.Context(), "SELECT 1 UNION SELECT 2 UNION SELECT 3")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var count int
	for rows.Next() {
		count++
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 rows, got %d", count)
	}

	ev := waitEvent(t, p.Events())
	if ev.Query != "SELECT 1 UNION SELECT 2 UNION SELECT 3" {
		t.Errorf("unexpected query: %q", ev.Query)
	}
}

func TestExecDDL(t *testing.T) {
	t.Parallel()
	upstream := startMySQL(t)
	p, addr := startProxy(t, upstream)
	db := openDB(t, addr)

	ctx := t.Context()
	_, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS _sql_tap_test (id INT PRIMARY KEY)")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}

	ev := waitEvent(t, p.Events())
	if ev.Op != proxy.OpQuery {
		t.Errorf("expected OpQuery, got %v", ev.Op)
	}
}

func TestInsertAffectedRows(t *testing.T) {
	t.Parallel()
	upstream := startMySQL(t)
	p, addr := startProxy(t, upstream)
	db := openDB(t, addr)

	ctx := t.Context()
	_, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS _sql_tap_test_ins (id INT PRIMARY KEY)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_ = waitEvent(t, p.Events()) // drain CREATE TABLE event

	_, err = db.ExecContext(ctx, "INSERT INTO _sql_tap_test_ins (id) VALUES (1), (2), (3)")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	ev := waitEvent(t, p.Events())
	if ev.RowsAffected != 3 {
		t.Errorf("expected 3 rows affected, got %d", ev.RowsAffected)
	}
}

func TestPreparedStatement(t *testing.T) {
	t.Parallel()
	upstream := startMySQL(t)
	p, addr := startProxy(t, upstream)
	db := openDB(t, addr)

	ctx := t.Context()
	stmt, err := db.PrepareContext(ctx, "SELECT ? + ?")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer func() { _ = stmt.Close() }()

	var result int
	if err := stmt.QueryRowContext(ctx, 1, 2).Scan(&result); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if result != 3 {
		t.Errorf("expected 3, got %d", result)
	}

	ev := waitEvent(t, p.Events())
	if ev.Op != proxy.OpExecute {
		t.Errorf("expected OpExecute, got %v", ev.Op)
	}
	if ev.Query != "SELECT ? + ?" {
		t.Errorf("unexpected query: %q", ev.Query)
	}
	if len(ev.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(ev.Args))
	}
	if ev.Args[0] != "1" {
		t.Errorf("expected arg[0]=%q, got %q", "1", ev.Args[0])
	}
	if ev.Args[1] != "2" {
		t.Errorf("expected arg[1]=%q, got %q", "2", ev.Args[1])
	}
}

func TestPreparedStatementStringArgs(t *testing.T) {
	t.Parallel()
	upstream := startMySQL(t)
	p, addr := startProxy(t, upstream)
	db := openDB(t, addr)

	ctx := t.Context()
	stmt, err := db.PrepareContext(ctx, "SELECT CONCAT(?, ?)")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer func() { _ = stmt.Close() }()

	var result string
	if err := stmt.QueryRowContext(ctx, "hello", "world").Scan(&result); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if result != "helloworld" {
		t.Errorf("expected %q, got %q", "helloworld", result)
	}

	ev := waitEvent(t, p.Events())
	if len(ev.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(ev.Args))
	}
	if ev.Args[0] != "hello" {
		t.Errorf("expected arg[0]=%q, got %q", "hello", ev.Args[0])
	}
	if ev.Args[1] != "world" {
		t.Errorf("expected arg[1]=%q, got %q", "world", ev.Args[1])
	}
}

func TestTransactionDetection(t *testing.T) {
	t.Parallel()
	upstream := startMySQL(t)
	p, addr := startProxy(t, upstream)
	db := openDB(t, addr)

	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	ev := waitEvent(t, p.Events())
	if ev.Op != proxy.OpBegin {
		t.Errorf("expected OpBegin, got %v", ev.Op)
	}
	txID := ev.TxID
	if txID == "" {
		t.Error("expected non-empty TxID")
	}

	_, err = tx.ExecContext(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("exec in tx: %v", err)
	}

	ev = waitEvent(t, p.Events())
	if ev.TxID != txID {
		t.Errorf("expected TxID %q, got %q", txID, ev.TxID)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	ev = waitEvent(t, p.Events())
	if ev.Op != proxy.OpCommit {
		t.Errorf("expected OpCommit, got %v", ev.Op)
	}
	if ev.TxID != txID {
		t.Errorf("expected TxID %q, got %q", txID, ev.TxID)
	}
}

func TestErrorCapture(t *testing.T) {
	t.Parallel()
	upstream := startMySQL(t)
	p, addr := startProxy(t, upstream)
	db := openDB(t, addr)

	_, err := db.ExecContext(t.Context(), "SELECT id FROM _nonexistent_table_12345")
	if err == nil {
		t.Fatal("expected error")
	}

	ev := waitEvent(t, p.Events())
	if ev.Error == "" {
		t.Error("expected non-empty error")
	}
}
