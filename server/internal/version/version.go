// Package version is the single source for ChronoDesk build identity defaults.
// Release builds may replace these values with -ldflags -X.
package version

const DefaultVersion = "0.3.0"

var (
	Version   = DefaultVersion
	Commit    = "unknown"
	BuildDate = "unknown"
)
