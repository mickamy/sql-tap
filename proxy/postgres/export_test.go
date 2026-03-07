package postgres

import (
	pgproto "github.com/jackc/pgproto3/v2"

	"github.com/mickamy/sql-tap/proxy"
)

// Exported wrappers for internal symbols used in package-external tests.

var DecodePGTimestampMicros = decodePGTimestampMicros

// DecodeBinaryParam exposes decodeBinaryParam for testing.
var DecodeBinaryParam = decodeBinaryParam

// OID constants for testing.
const (
	OIDTimestamp   = oidTimestamp
	OIDTimestampTZ = oidTimestampTZ
)

// TestConn wraps conn for protocol-level unit tests.
type TestConn struct{ c *conn }

// NewTestConn creates a minimal conn for testing the extended query flow.
func NewTestConn() *TestConn {
	return &TestConn{c: &conn{
		preparedStmts:    make(map[string]string),
		preparedStmtOIDs: make(map[string][]uint32),
		events:           make(chan<- proxy.Event, 16),
	}}
}

func (tc *TestConn) HandleParse(name, query string, oids []uint32) {
	tc.c.handleParse(&pgproto.Parse{Name: name, Query: query, ParameterOIDs: oids})
}

func (tc *TestConn) HandleDescribe(name string) {
	tc.c.handleDescribe(&pgproto.Describe{ObjectType: 'S', Name: name})
}

func (tc *TestConn) HandleParameterDescription(oids []uint32) {
	tc.c.handleParameterDescription(&pgproto.ParameterDescription{ParameterOIDs: oids})
}

func (tc *TestConn) HandleBind(stmtName string, params [][]byte, formatCodes []int16) {
	tc.c.handleBind(&pgproto.Bind{
		PreparedStatement:    stmtName,
		Parameters:           params,
		ParameterFormatCodes: formatCodes,
	})
}

func (tc *TestConn) HandleReadyForQuery() {
	tc.c.drainPendingDescribes()
}

func (tc *TestConn) LastBindArgs() []string {
	return tc.c.lastBindArgs
}
