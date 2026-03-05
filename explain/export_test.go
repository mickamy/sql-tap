package explain

// Exported wrappers for internal symbols used in package-external tests.

var (
	BuildAnyArgs         = buildAnyArgs
	ParseTimestampParams = parseTimestampParams
	ParsePGTimestamp     = parsePGTimestamp
)

const PgEpochUnix = pgEpochUnix
