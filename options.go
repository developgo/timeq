package timeq

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/sahib/timeq/item"
)

type SyncMode int

func (sm SyncMode) IsValid() bool {
	return sm >= 0 && sm <= SyncFull
}

// The available option are inspired by SQLite:
// https://www.sqlite.org/pragma.html#pragma_synchronous
const (
	// SyncNone does not sync on normal operation (only on close)
	SyncNone = SyncMode(0)
	// SyncData only synchronizes the data log
	SyncData = SyncMode(1 << iota)
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

type writerLogger struct {
	w io.Writer
}

func (fl *writerLogger) Printf(fmtStr string, args ...any) {
	fmt.Fprintf(fl.w, "[timeq] "+fmtStr+"\n", args...)
}

type ErrorMode int

func (em ErrorMode) IsValid() bool {
	return em < errorModeMax && em >= 0
}

const (
	// ErrorModeAbort will immediately abort the current
	// operation if an error is encountered that might lead to data loss.
	ErrorModeAbort = ErrorMode(iota)

	// ErrorModeContinue tries to progress further in case of errors
	// by jumping over a faulty bucket or entry in a
	// If the error was recoverable, none is returned, but the
	// Logger in the Options will be called (if set) to log the error.
	ErrorModeContinue

	errorModeMax
)

func WriterLogger(w io.Writer) Logger {
	return &writerLogger{w: w}
}

// DefaultLogger produces a logger that writes to stderr.
func DefaultLogger() Logger {
	return &writerLogger{w: os.Stderr}
}

// NullLogger produces a logger that discards all messages.
func NullLogger() Logger {
	return &writerLogger{w: io.Discard}
}

var (
	// ErrChangedSplitFunc is returned when the configured split func
	// in options does not fit to the state on disk.
	ErrChangedSplitFunc = errors.New("split func changed")
)

// BucketSplitConf defines what keys are sorted in what bucket.
// See Options.BucketSplitConf for more info.
type BucketSplitConf struct {
	// Func is the function that does the splitting.
	Func func(item.Key) item.Key

	// Name is used as identifier to figure out
	// when the disk split func changed.
	Name string
}

// Options gives you some knobs to configure the queue.
// Read the individual options carefully, as some of them
// can only be set on the first call to Open()
type Options struct {
	// SyncMode controls how often we sync data to the disk. The more data we sync
	// the more durable is the queue at the cost of throughput.
	// Default is the safe SyncFull. Think twice before lowering this.
	SyncMode SyncMode

	// Logger is used to output some non-critical warnigns or errors that could
	// have been recovered. By default we print to stderr.
	// Only warnings or errors are logged, no debug or informal messages.
	Logger Logger

	// ErrorMode defines how non-critical errors are handled.
	// See the individual enum values for more info.
	ErrorMode ErrorMode

	// BucketSplitConf defines what key goes to what bucket.
	// The provided function should clamp the key value to
	// a common value. Each same value that was returned goes
	// into the same  The returned value should be also
	// the minimum key of the
	//
	// Example: '(key / 10) * 10' would produce buckets with 10 items.
	//
	// What bucket size to choose? Please refer to the FAQ in the README.
	//
	// NOTE: This may not be changed after you opened a queue with it!
	//       Only way to change is to create a new queue and shovel the
	//       old data into it.
	BucketSplitConf BucketSplitConf

	// MaxParallelOpenBuckets limits the number of buckets that can be opened
	// in parallel. Normally, operations like Push() will create more and more
	// buckets with time and old buckets do not get closed automatically, as
	// we don't know when they get accessed again. If there are more buckets
	// open than this number they get closed and will be re-opened if accessed
	// again. If this happens frequently, this comes with a performance penalty.
	// If you tend to access your data with rather random keys, you might want
	// to increase this number, depending on how much resources you have.
	//
	// If this number is <= 0, then this feature is disabled, which is not
	// recommended.
	MaxParallelOpenBuckets int
}

// DefaultOptions give you a set of options that are good to enough to try some
// experiments. Your mileage can vary a lot with different settings, so make
// sure to do some benchmarking!
func DefaultOptions() Options {
	return Options{
		SyncMode:               SyncFull,
		ErrorMode:              ErrorModeAbort,
		Logger:                 DefaultLogger(),
		BucketSplitConf:        DefaultBucketSplitConf,
		MaxParallelOpenBuckets: 4,
	}
}

// DefaultBucketSplitConf assumes that `key` is a nanosecond unix timestamps
// and divides data (roughly) in 2m minute buckets.
var DefaultBucketSplitConf = ShiftBucketSplitConf(37)

// ShiftBucketSplitConf creates a fast BucketSplitConf that divides data into buckets
// by masking `shift` less significant bits of the key. With a shift
// of 37 you roughly get 2m buckets (if your key input are nanosecond-timestamps).
// If you want to calculate the size of a shift, use this formula:
// (2 ** shift) / (1e9 / 60) = minutes
func ShiftBucketSplitConf(shift int) BucketSplitConf {
	timeMask := ^item.Key(0) << shift
	return BucketSplitConf{
		Name: fmt.Sprintf("shift:%d", shift),
		Func: func(key item.Key) item.Key {
			return key & timeMask
		},
	}
}

// FixedSizeBucketSplitConf returns a BucketSplitConf that divides buckets into
// equal sized buckets with `n` entries. This can also be used to create
// time-based keys, if you use nanosecond based keys and pass time.Minute
// to create a buckets with a size of one minute.
func FixedSizeBucketSplitConf(n uint64) BucketSplitConf {
	if n == 0 {
		// avoid zero division.
		n = 1
	}

	return BucketSplitConf{
		Name: fmt.Sprintf("fixed:%d", n),
		Func: func(key item.Key) item.Key {
			return (key / item.Key(n)) * item.Key(n)
		},
	}
}

func (o *Options) Validate() error {
	if o.Logger == nil {
		// this allows us to leave out quite some null checks when
		// using the logger option, even when it's not set.
		o.Logger = NullLogger()
	}

	if !o.SyncMode.IsValid() {
		return errors.New("invalid sync mode")
	}

	if !o.ErrorMode.IsValid() {
		return errors.New("invalid error mode")
	}

	if o.BucketSplitConf.Func == nil {
		return errors.New("bucket func is not allowed to be empty")
	}

	if o.MaxParallelOpenBuckets == 0 {
		// For the outside, that's the same thing, but closeUnused() internally
		// actually knows how to keep the number of buckets to zero, so be clear
		// that the user wants to disable this feature.
		o.MaxParallelOpenBuckets = -1
	}

	return nil
}
