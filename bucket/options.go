package bucket

import (
	"fmt"
	"io"
	"os"
)

type SyncMode int

// The available option are inspired by SQLite:
// https://www.sqlite.org/pragma.html#pragma_synchronous
const (
	// SyncNone does not sync on normal operation (only on close)
	SyncNone = SyncMode(1 << iota)
	// SyncData only synchronizes the data log
	SyncData
	// SyncIndex only synchronizes the index log (does not make sense alone)
	SyncIndex
	// SyncFull syncs both the data and index log
	SyncFull = SyncData | SyncIndex
)

// Logger is a small interface to redirect logs to.
// The default logger outputs to stderr.
type Logger interface {
	Printf(fmt string, args ...any)
}

type fileLogger struct {
	w io.Writer
}

func (fl *fileLogger) Printf(fmtStr string, args ...any) {
	fmt.Fprintf(fl.w, "[timeq] "+fmtStr, args...)
}

type ErrorMode int

const (
	ErrorModeAbort = ErrorMode(iota)
	ErrorModeContinue
)

func DefaultLogger() Logger {
	return &fileLogger{w: os.Stderr}
}

// Options are fine-tuning knobs specific to individual buckets
type Options struct {
	// SyncMode controls how often we sync data to the disk. The more data we sync
	// the more durable is the queue at the cost of throughput.
	// Default is the safe SyncFull. Think twice before lowering this.
	SyncMode SyncMode

	// Logger is used to output some non-critical warnigns or errors that could
	// have been recovered. By default we print to stderr.
	Logger Logger

	// ErrorMode defines how non-critical errors are handled.
	// See the individual enum values for more info.
	ErrorMode ErrorMode
}

// DefaultOptions returns sane options you can start with
func DefaultOptions() Options {
	return Options{
		SyncMode:  SyncFull,
		ErrorMode: ErrorModeAbort,
		Logger:    DefaultLogger(),
	}
}
