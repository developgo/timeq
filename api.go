package timeq

import (
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

// DefaultBucketFunc assumes that `key` is a nanosecond unix timestamps
// and divides data (roughly) in 15 minute buckets.
func DefaultBucketFunc(key Key) Key {
	// This should yield roughly 9m buckets for nanosecond timestamps.
	// (and saves us expensive divisions)
	return key & (^item.Key(0) << 39)
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
	// the minimm key of the bucket.
	//
	// Example: '(key / 10) * 10' would produce buckets with 10 items.
	//
	// NOTE: This may not be changed after you opened a queue with it!
	//       Only way to change is to create a new queue and shovel the
	//       old data into it.
	BucketFunc func(Key) Key
}

// DefaultOptions give you a set of options that are good to enough
// to try some expirements. Your mileage can vary a lot with different settings!
func DefaultOptions() Options {
	return Options{
		Options:    bucket.DefaultOptions(),
		BucketFunc: DefaultBucketFunc,
	}
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
	bs, err := bucket.LoadAll(dir, opts.Options)
	if err != nil {
		return nil, fmt.Errorf("buckets: %w", err)
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
		buck, err := q.buckets.ForKey(keyMod)
		if err != nil {
			return fmt.Errorf("bucket: for-key: %w", err)
		}

		nextIdx := binsplit(items, keyMod, q.opts.BucketFunc)
		if err := buck.Push(items[:nextIdx]); err != nil {
			return fmt.Errorf("bucket: push: %w", err)
		}

		items = items[nextIdx:]
	}

	return nil
}

// Pop fetches up to `n` items from the queue. It will only return
// less items if the queue does not hold more items. If an error
// occured no items are returned. If n < 0 then as many items as possible
// will be returned - this is not recommended as we call it the YOLO mode.
//
// The `dst` argument can be used to pass a pre-allocated slice that
// the queue appends to. This can be done to avoid allocations.
// If you don't care you can also pass nil.
//
// You should immediately process the items and not store them
// elsewhere. The reason is that the returned memory is a slice of a
// memory-mapped file. Certain operations like Clear() will close
// those mappings, causing segfaults when still accessing them. If you
// need the items for later, then use item.Copy()
func (q *Queue) Pop(n int, dst Items) (Items, error) {
	if n < 0 {
		// use max value in this case:
		n = int(^uint(0) >> 1)
	}

	// NOTE: We can't check here if a bucket is empty and delete if
	// afterwards. Otherwise the mmap would be closed and accessing
	// items we returned can cause a segfault immediately.

	var count = n
	err := q.buckets.Iter(func(b *bucket.Bucket) error {
		newDst, popped, err := b.Pop(count, dst)
		if err != nil {
			return err
		}

		dst = newDst
		count -= popped
		if count <= 0 {
			return bucket.IterStop
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return dst, nil
}

// DeleteLowerThan deletes all items lower than `key`.
func (q *Queue) DeleteLowerThan(key Key) (int, error) {
	var numDeleted int
	var deletableBucks []*bucket.Bucket

	err := q.buckets.Iter(func(bucket *bucket.Bucket) error {
		numDeletedOfBucket, err := bucket.DeleteLowerThan(key)
		if err != nil {
			return err
		}

		numDeleted += numDeletedOfBucket
		if bucket.Empty() {
			deletableBucks = append(deletableBucks, bucket)
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
// afterwards. If items in the `dst` queue exists with the same timestamp, then they
// will be overwritten.
//
// This method can be used if you want to change options like the BucketFunc or if you
// intend to have more than one queue that are connected by some logic. Examples for the
// latter case would be a "deadletter queue" where you put failed calculations for later
// recalucations or a queue for unacknowledged items.
//
// NOTE: This operation is currently not implemented atomic. Data might be lost
// if a crash occurs between pop and push.
func Shovel(src, dst *Queue) (int, error) {
	buf := make(Items, 0, 2000)
	numPopped := 0
	for {
		items, err := src.Pop(cap(buf), buf[:0])
		if err != nil {
			return numPopped, fmt.Errorf("timeq: shovel-pop: %w", err)
		}

		numPopped += len(items)
		if len(items) == 0 {
			break
		}

		if err := dst.Push(items); err != nil {
			return numPopped, fmt.Errorf("timeq: shovel-push: %w", err)
		}

		if len(items) < cap(buf) {
			break
		}
	}

	return numPopped, nil
}
