package timeq

import (
	"errors"
	"fmt"
	"slices"

	"github.com/sahib/timeq/bucket"
	"github.com/sahib/timeq/item"
)

// Item is a single item that you push or pop from the queue.
type Item = item.Item

// Items is a list of items.
type Items = item.Items

// Key is the priority of each item in the queue.
// Lower keys will be popped first.
type Key = item.Key

// ReadFn is the type for functions passed to Pop/Move/Peek
type ReadFn = bucket.ReadFn

// DefaultBucketFunc assumes that `key` is a nanosecond unix timestamps
// and divides data (roughly) in 2m minute buckets.
var DefaultBucketFunc = ShiftBucketFunc(37)

// ShiftBucketFunc creates a fast BucketFunc that divides data into buckets
// by masking `shift` less significant bits of the key. With a shift
// of 37 you roughly get 2m buckets (if your key input are nanosecond-timestamps).
// If you want to calculate the size of a shift, use this formula:
// (2 ** shift) / (1e9 / 60) = minutes
func ShiftBucketFunc(shift int) func(key Key) Key {
	timeMask := ^item.Key(0) << shift
	return func(key Key) Key {
		return key & timeMask
	}
}

// FixedSizeBucketFunc returns a BucketFunc that divides buckets into
// equal sized buckets with `n` entries. This can also be used to create
// time-based keys, if you use nanosecond based keys and pass time.Minute
// to create a buckets with a size of one minute.
func FixedSizeBucketFunc(n uint64) func(key Key) Key {
	if n == 0 {
		// avoid zero division.
		n = 1
	}

	return func(key Key) Key {
		return (key / item.Key(n)) * item.Key(n)
	}
}

// Options gives you some knobs to configure the queue.
// Read the individual options carefully, as some of them
// can only be set on the first call to Open()
type Options struct {
	bucket.Options

	// BucketFunc defines what key goes to what bucket.
	// The provided function should clamp the key value to
	// a common value. Each same value that was returned goes
	// into the same bucket. The returned value should be also
	// the minimum key of the bucket.
	//
	// Example: '(key / 10) * 10' would produce buckets with 10 items.
	//
	// NOTE: This may not be changed after you opened a queue with it!
	//       Only way to change is to create a new queue and shovel the
	//       old data into it.
	BucketFunc func(Key) Key

	// MaxParallelOpenBuckets limits the number of buckets that can be opened
	// in parallel. Normally, operations like Push() will create more and more
	// buckets with time and old buckets do not get closed automatically, as
	// we don't know when they get accessed again. If there are more buckets
	// open than this number they get closed and will be re-opened if accessed
	// again. If this happens frequently, this comes with a performance penalty.
	// If you tend to access your data with rather random keys, you might want
	// to increase this number, depending on how much resources you have.
	//
	// This is currently only applies to write operations (i.e. Push() and so on),
	// not for read operations like Pop() (as memory needs to stay intact when
	// reading from several buckets). If you have situations where you do read-only
	// operations for some time you should throw in a CloseUnused() call from time
	// to time to make sure memory gets cleaned up.
	//
	// If this number is <= 0, then this feature is disabled, which is not
	// recommended.
	MaxParallelOpenBuckets int
}

// DefaultOptions give you a set of options that are good to enough
// to try some expirements. Your mileage can vary a lot with different settings!
func DefaultOptions() Options {
	return Options{
		Options:                bucket.DefaultOptions(),
		BucketFunc:             DefaultBucketFunc,
		MaxParallelOpenBuckets: 3,
	}
}

func (o *Options) Validate() error {
	if o.Logger == nil {
		// this allows us to leave out quite some null checks when
		// using the logger option, even when it's not set.
		o.Logger = bucket.NullLogger()
	}

	if o.BucketFunc == nil {
		return errors.New("bucket func is not allowed to be empty")
	}

	return nil
}

// Queue is the high level API to the priority queue.
type Queue struct {
	buckets *bucket.Buckets
	opts    Options
}

// Open tries to open the priority queue structure in `dir`.
// If `dir` does not exist, then a new, empty priority queue is created.
// The behavior of the queue can be fine-tuned with `opts`.
func Open(dir string, opts Options) (*Queue, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	bs, err := bucket.LoadAll(dir, opts.MaxParallelOpenBuckets, opts.Options)
	if err != nil {
		return nil, fmt.Errorf("buckets: %w", err)
	}

	if err := bs.ValidateBucketKeys(opts.BucketFunc); err != nil {
		return nil, err
	}

	return &Queue{
		opts:    opts,
		buckets: bs,
	}, nil
}

// binsplit returns the first index of `items` that would
// not go to the bucket `comp`. There are two assumptions:
//
// * "items" is not empty.
// * "comp" exists for at least one fn(item.Key)
// * The first key in `items` must be fn(key) == comp
//
// If assumptions are not fulfilled you will get bogus results.
func binsplit(items Items, comp Key, fn func(Key) Key) int {
	l := len(items)
	if l == 0 {
		return 0
	}
	if l == 1 {
		return 1
	}

	pivotIdx := l / 2
	pivotKey := fn(items[pivotIdx].Key)
	if pivotKey != comp {
		// search left:
		return binsplit(items[:pivotIdx], comp, fn)
	}

	// search right:
	return pivotIdx + binsplit(items[pivotIdx:], comp, fn)
}

// Push pushes a batch of `items` to the queue.
func (q *Queue) Push(items Items) error {
	if len(items) == 0 {
		return nil
	}

	slices.SortFunc(items, func(i, j item.Item) int {
		return int(i.Key - j.Key)
	})

	// Sort items into the respective buckets:
	for len(items) > 0 {
		keyMod := q.opts.BucketFunc(items[0].Key)
		nextIdx := binsplit(items, keyMod, q.opts.BucketFunc)
		buck, err := q.buckets.ForKey(keyMod)
		if err != nil {
			if q.opts.ErrorMode == bucket.ErrorModeAbort {
				return fmt.Errorf("bucket: for-key: %w", err)
			}

			q.opts.Logger.Printf("failed to push: %v", err)
		} else {
			if err := buck.Push(items[:nextIdx]); err != nil {
				if q.opts.ErrorMode == bucket.ErrorModeAbort {
					return fmt.Errorf("bucket: push: %w", err)
				}

				q.opts.Logger.Printf("failed to push: %v", err)
			}
		}

		items = items[nextIdx:]
	}

	return nil
}

// Pop fetches up to `n` items from the queue. It will only return
// less items if the queue does not hold more items. If an error
// occurred no items are returned. If n < 0 then as many items as possible
// will be returned - this is not recommended as we call it the YOLO mode.
//
// The `dst` argument can be used to pass a pre-allocated slice that
// the queue appends to. This can be done to avoid allocations.
// If you don't care you can also pass nil.
//
// You should immediately process the items and not store them
// elsewhere. The reason is that the returned memory is a slice of a
// memory-mapped file. Certain operations like Clear(), Push(),
// DeleteLowerThan() and Shovel() will close or move those mappings,
// causing segfaults when still accessing them. If you need the items
// for later, then use item.Copy() before your next call.
func (q *Queue) Pop(n int, dst Items, fn ReadFn) error {
	return q.buckets.Read(bucket.ReadOpPop, n, dst, fn, nil)
}

// Peek works like Pop, but does not delete the items in the queue.
// Note that calling Peek() twice will yield the same result.
func (q *Queue) Peek(n int, dst Items, fn ReadFn) error {
	return q.buckets.Read(bucket.ReadOpPeek, n, dst, fn, nil)
}

// Move works like Pop, but does push the popped items to `dstQueue`.
// This is its own operation since the data is only deleted from `q`
// when the push was synced on `dstQueue`. This is safer than using
// the Push/Pop itself.
func (q *Queue) Move(n int, dst Items, dstQueue *Queue, fn ReadFn) error {
	return q.buckets.Read(bucket.ReadOpMove, n, dst, fn, dstQueue.buckets)
}

// DeleteLowerThan deletes all items lower than `key`.
func (q *Queue) DeleteLowerThan(key Key) (int, error) {
	var numDeleted int
	var deletableBucks []*bucket.Bucket

	// TODO: Move that to buckets.go for consistency reasons?

	err := q.buckets.Iter(bucket.Load, func(bucketKey item.Key, buck *bucket.Bucket) error {
		if bucketKey >= key {
			// stop loading buckets if not necessary.
			return bucket.IterStop
		}

		numDeletedOfBucket, err := buck.DeleteLowerThan(key)
		if err != nil {
			if q.opts.ErrorMode == bucket.ErrorModeAbort {
				return err
			}

			// try with the next bucket in the hope that it works:
			q.opts.Logger.Printf("failed to delete : %v", err)
			return nil
		}

		numDeleted += numDeletedOfBucket
		if buck.Empty() {
			deletableBucks = append(deletableBucks, buck)
		}

		return nil
	})

	if err != nil {
		return numDeleted, err
	}

	for _, bucket := range deletableBucks {
		if err := q.buckets.Delete(bucket.Key()); err != nil {
			return numDeleted, fmt.Errorf("bucket delete: %w", err)
		}
	}

	return numDeleted, nil
}

// Len returns the number of items in the queue.
// NOTE: This is a little more expensive than a simple getter.
// You should avoid calling this in a hot loop.
func (q *Queue) Len() int {
	return q.buckets.Len()
}

// Sync can be called to explicitly sync the queue contents
// to persistent storage, even if you configured SyncNone.
func (q *Queue) Sync() error {
	return q.buckets.Sync()
}

// Clear fully deletes the queue contents.
func (q *Queue) Clear() error {
	return q.buckets.Clear()
}

// Close should always be called and error checked when you're done
// with using the queue. Close might still flush out some data, depending
// on what sync mode you configured.
func (q *Queue) Close() error {
	return q.buckets.Close()
}

// Shovel moves items from `src` to `dst`. The `src` queue will be completely drained
// afterwards. For speed reasons this assume that the dst queue uses the same bucket func
// as the source queue. If you cannot guarantee this, you should implement a naive Shovel()
// implementation that just uses Pop/Push.
//
// This method can be used if you want to change options like the BucketFunc or if you
// intend to have more than one queue that are connected by some logic. Examples for the
// latter case would be a "deadletter queue" where you put failed calculations for later
// recalucations or a queue for unacknowledged items.
func Shovel(src, dst *Queue) (int, error) {
	n, err := src.buckets.Shovel(dst.buckets)
	if err != nil {
		return n, err
	}

	return n, nil
}
