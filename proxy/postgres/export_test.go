package postgres

// Exported wrappers for internal symbols used in package-external tests.

var DecodePGTimestampMicros = decodePGTimestampMicros

// DecodeBinaryParam exposes decodeBinaryParam for testing.
var DecodeBinaryParam = decodeBinaryParam

// OID constants for testing.
const (
	OidTimestamp   = oidTimestamp
	OidTimestampTZ = oidTimestampTZ
)
