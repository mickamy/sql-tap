package proxy

import (
	"context"
	"fmt"
	"time"
)

// Op represents the type of database operation captured.
type Op int32

const (
	OpQuery    Op = iota // Simple query or extended-query execute
	OpExec               // Non-query execution
	OpPrepare            // Prepared statement parse
	OpBind               // Parameter binding
	OpExecute            // Extended-protocol execute
	OpBegin              // Transaction begin
	OpCommit             // Transaction commit
	OpRollback           // Transaction rollback
)

func (o Op) String() string {
	switch o {
	case OpQuery:
		return "Query"
	case OpExec:
		return "Exec"
	case OpPrepare:
		return "Prepare"
	case OpBind:
		return "Bind"
	case OpExecute:
		return "Execute"
	case OpBegin:
		return "Begin"
	case OpCommit:
		return "Commit"
	case OpRollback:
		return "Rollback"
	}
	return fmt.Sprintf("UnknownOp(%d)", o)
}

// Event represents a captured database query event.
type Event struct {
	ID              string
	Op              Op
	Query           string
	Args            []string
	StartTime       time.Time
	Duration        time.Duration
	RowsAffected    int64
	Error           string
	TxID            string
	NPlus1          bool
	NormalizedQuery string
}

// Proxy is the common interface for DB protocol proxies.
type Proxy interface {
	// ListenAndServe accepts client connections and relays them to the upstream DB.
	ListenAndServe(ctx context.Context) error
	// Events returns the channel of captured events.
	Events() <-chan Event
	// Close stops the proxy.
	Close() error
}
