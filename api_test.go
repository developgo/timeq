package timeq

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sahib/timeq/item"
	"github.com/sahib/timeq/item/testutils"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyTrunc(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2023, 1, 1, 12, 13, 14, 15, time.UTC)
	trunc1 := DefaultBucketFunc(item.Key(stamp.UnixNano()))
	trunc2 := DefaultBucketFunc(item.Key(stamp.Add(time.Minute).UnixNano()))
	trunc3 := DefaultBucketFunc(item.Key(stamp.Add(time.Hour).UnixNano()))

	// First two stamps only differ by one minute. They should be truncated
	// to the same value. One hour further should yield a different value.
	require.Equal(t, trunc1, trunc2)
	require.NotEqual(t, trunc1, trunc3)
}

func TestAPIPushPopEmpty(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	queue, err := Open(dir, DefaultOptions())
	require.NoError(t, err)
	require.NoError(t, queue.Close())

	require.NoError(t, queue.Push(nil))
	err = queue.Read(100, nil, func(items Items) (ReadOp, error) {
		return ReadOpPop, errors.New("I was called!")
	})

	require.NoError(t, err)
}

func TestAPIOptionsValidate(t *testing.T) {
	opts := Options{}
	require.Error(t, opts.Validate())

	opts.BucketFunc = func(k Key) Key { return k }
	require.NoError(t, opts.Validate())
	require.NotNil(t, opts.Logger)
}

func TestAPIPushPopSeveralBuckets(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Open queue with a bucket size of 10 items:
	opts := DefaultOptions()
	opts.BucketFunc = func(key Key) Key {
		return (key / 10) * 10
	}

	queue, err := Open(dir, opts)
	require.NoError(t, err)

	// Push two batches:
	push1 := Items(testutils.GenItems(10, 20, 1))
	push2 := Items(testutils.GenItems(30, 40, 1))
	require.NoError(t, queue.Push(push1))
	require.NoError(t, queue.Push(push2))
	require.Equal(t, 20, queue.Len())

	// Read them in one go:
	got := Items{}
	require.NoError(t, queue.Read(-1, nil, func(items Items) (ReadOp, error) {
		got = append(got, items.Copy()...)
		return ReadOpPop, nil
	}))

	require.Equal(t, 0, queue.Len())
	require.Len(t, got, 20)
	require.Equal(t, append(push1, push2...), got)

	// Write the queue to disk:
	require.NoError(t, queue.Sync())
	require.NoError(t, queue.Close())

	// Re-open to see if the items were permanently deleted:
	reopened, err := Open(dir, opts)
	require.NoError(t, err)
	require.Equal(t, 0, reopened.Len())
	require.NoError(t, reopened.Read(-1, nil, func(items Items) (ReadOp, error) {
		require.Empty(t, items)
		return ReadOpPop, nil
	}))
}

func TestAPIShovelFastPath(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	q1Dir := filepath.Join(dir, "q1")
	q2Dir := filepath.Join(dir, "q2")

	q1, err := Open(q1Dir, DefaultOptions())
	require.NoError(t, err)

	q2, err := Open(q2Dir, DefaultOptions())
	require.NoError(t, err)

	exp := Items(testutils.GenItems(0, 1000, 1))
	require.NoError(t, q1.Push(exp))
	require.Equal(t, len(exp), q1.Len())
	require.Equal(t, 0, q2.Len())

	n, err := q1.Shovel(q2)
	require.NoError(t, err)
	require.Equal(t, len(exp), n)

	require.Equal(t, 0, q1.Len())
	require.Equal(t, len(exp), q2.Len())

	require.NoError(t, q2.Read(len(exp), nil, func(got Items) (ReadOp, error) {
		require.Equal(t, exp, got)
		return ReadOpPop, nil
	}))

	require.NoError(t, q1.Close())
	require.NoError(t, q2.Close())
}

func TestAPIShovelSlowPath(t *testing.T) {
	t.Run("reopen", func(t *testing.T) {
		testAPIShovelSlowPath(t, true)
	})

	t.Run("no-reopen", func(t *testing.T) {
		testAPIShovelSlowPath(t, false)
	})
}

func testAPIShovelSlowPath(t *testing.T, reopen bool) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	q1Dir := filepath.Join(dir, "q1")
	q2Dir := filepath.Join(dir, "q2")

	q1, err := Open(q1Dir, DefaultOptions())
	require.NoError(t, err)

	q2, err := Open(q2Dir, DefaultOptions())
	require.NoError(t, err)

	q1Push := Items(testutils.GenItems(0, 500, 1))
	require.NoError(t, q1.Push(q1Push))

	// If the bucket exists we have to append:
	q2Push := Items(testutils.GenItems(1000, 2500, 1))
	require.NoError(t, q2.Push(q2Push))

	require.Equal(t, len(q1Push), q1.Len())
	require.Equal(t, len(q2Push), q2.Len())

	n, err := q1.Shovel(q2)
	require.NoError(t, err)
	require.Equal(t, len(q1Push), n)

	require.Equal(t, 0, q1.Len())
	require.Equal(t, len(q1Push)+len(q2Push), q2.Len())

	if reopen {
		require.NoError(t, q1.Close())
		require.NoError(t, q2.Close())

		q1, err = Open(q1Dir, DefaultOptions())
		require.NoError(t, err)

		q2, err = Open(q2Dir, DefaultOptions())
		require.NoError(t, err)
	}

	exp := append(q1Push, q2Push...)
	require.NoError(t, q2.Read(len(q1Push)+len(q2Push), nil, func(got Items) (ReadOp, error) {
		require.Equal(t, exp, got)
		return ReadOpPop, nil
	}))

	require.NoError(t, q1.Close())
	require.NoError(t, q2.Close())
}

func TestAPIDelete(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	opts := DefaultOptions()
	opts.BucketFunc = func(k Key) Key {
		return (k / 100) * 100
	}

	queue, err := Open(dir, opts)
	require.NoError(t, err)

	// Deleting the first half should work without issue:
	exp := testutils.GenItems(0, 1000, 1)
	require.NoError(t, queue.Push(exp))
	ndeleted, err := queue.Delete(0, 500)
	require.NoError(t, err)
	require.Equal(t, 501, ndeleted)

	// Deleting the same should yield 0 now.
	ndeleted, err = queue.Delete(0, 500)
	require.NoError(t, err)
	require.Equal(t, 0, ndeleted)

	// Do a partial delete of a bucket:
	ndeleted, err = queue.Delete(0, 501)
	require.NoError(t, err)
	require.Equal(t, 1, ndeleted)

	// Delete more than what is left:
	ndeleted, err = queue.Delete(0, 2000)
	require.NoError(t, err)
	require.Equal(t, 498, ndeleted)

	// Try with a fork:
	f, err := queue.Fork("fork")
	require.NoError(t, err)
	ndeleted, err = f.Delete(0, 2000)
	require.NoError(t, err)
	require.Equal(t, 0, ndeleted)

	require.NoError(t, queue.Close())
}

func TestAPIPeek(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	opts := DefaultOptions()
	opts.BucketFunc = func(k Key) Key {
		return (k / 100) * 100
	}

	queue, err := Open(dir, opts)
	require.NoError(t, err)

	exp := testutils.GenItems(0, 200, 1)
	require.NoError(t, queue.Push(exp))

	got, err := PeekCopy(queue, len(exp))
	require.NoError(t, err)
	require.Equal(t, len(exp), len(got))
	require.Equal(t, exp, got)

	// Check that Peek() really did not delete anything:
	got = got[:0]
	require.NoError(t, queue.Read(len(exp), nil, func(items Items) (ReadOp, error) {
		got = append(got, items.Copy()...)
		return ReadOpPop, nil
	}))

	require.Equal(t, len(exp), len(got))
	require.Equal(t, exp, got)
	require.NoError(t, queue.Close())
}

func TestAPClear(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	queue, err := Open(dir, DefaultOptions())
	require.NoError(t, err)

	// Empty clear should still work fine:
	require.NoError(t, queue.Clear())
	require.NoError(t, queue.Push(testutils.GenItems(0, 100, 1)))
	require.NoError(t, queue.Clear())
	require.Equal(t, 0, queue.Len())

	require.NoError(t, queue.Close())
}

func TestAPIMove(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	srcDir := filepath.Join(dir, "src")
	dstDir := filepath.Join(dir, "dst")

	opts := DefaultOptions()
	opts.BucketFunc = func(k Key) Key {
		return (k / 100) * 100
	}

	srcQueue, err := Open(srcDir, opts)
	require.NoError(t, err)

	dstQueue, err := Open(dstDir, opts)
	require.NoError(t, err)

	exp := testutils.GenItems(0, 200, 1)
	require.NoError(t, srcQueue.Push(exp))
	require.Equal(t, len(exp), srcQueue.Len())
	require.Equal(t, 0, dstQueue.Len())

	var got Items
	require.NoError(t, srcQueue.Read(len(exp), nil, func(items Items) (ReadOp, error) {
		got = append(got, items.Copy()...)
		return ReadOpPop, dstQueue.Push(items)
	}))

	require.NoError(t, err)
	require.Equal(t, len(exp), len(got))
	require.Equal(t, exp, got)

	require.Equal(t, 0, srcQueue.Len())
	require.Equal(t, len(exp), dstQueue.Len())

	gotMoved := Items{}
	require.NoError(t, dstQueue.Read(len(exp), nil, func(items Items) (ReadOp, error) {
		gotMoved = append(gotMoved, items.Copy()...)
		return ReadOpPop, nil
	}))

	require.Equal(t, exp, gotMoved)

	require.Equal(t, 0, srcQueue.Len())
	require.Equal(t, 0, dstQueue.Len())

	require.NoError(t, srcQueue.Close())
	require.NoError(t, dstQueue.Close())
}

type LogBuffer struct {
	buf bytes.Buffer
}

func (lb *LogBuffer) Printf(fmtSpec string, args ...any) {
	lb.buf.WriteString(fmt.Sprintf(fmtSpec, args...))
	lb.buf.WriteByte('\n')
}

func (lb *LogBuffer) String() string {
	return lb.buf.String()
}

func TestAPIErrorModePush(t *testing.T) {
	t.Run("abort", func(t *testing.T) {
		testAPIErrorModePush(t, ErrorModeAbort)
	})
	t.Run("continue", func(t *testing.T) {
		testAPIErrorModePush(t, ErrorModeContinue)
	})
}

func testAPIErrorModePush(t *testing.T, mode ErrorMode) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	logger := &LogBuffer{}
	opts := Options{
		ErrorMode: mode,
		Logger:    logger,
		BucketFunc: func(key Key) Key {
			return (key / 10) * 10
		},
	}

	queue, err := Open(dir, opts)
	require.NoError(t, err)

	// make sure the whole directory cannot be accessed,
	// forcing an error during Push.
	require.NoError(t, os.Chmod(dir, 0100))

	pushErr := queue.Push(testutils.GenItems(0, 100, 1))
	if mode == ErrorModeContinue {
		require.NotEmpty(t, logger.String())
		require.NoError(t, pushErr)
	} else {
		require.Error(t, pushErr)
	}

	require.NoError(t, queue.Close())
}

func TestAPIErrorModePop(t *testing.T) {
	t.Run("abort", func(t *testing.T) {
		testAPIErrorModePop(t, ErrorModeAbort)
	})
	t.Run("continue", func(t *testing.T) {
		testAPIErrorModePop(t, ErrorModeContinue)
	})
}

func testAPIErrorModePop(t *testing.T, mode ErrorMode) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	logger := &LogBuffer{}
	opts := Options{
		ErrorMode: mode,
		Logger:    logger,
		BucketFunc: func(key Key) Key {
			return (key / 10) * 10
		},
	}

	queue, err := Open(dir, opts)
	require.NoError(t, err)

	require.NoError(t, queue.Push(testutils.GenItems(0, 100, 1)))

	// truncate the data log of a single
	require.NoError(
		t,
		os.Truncate(filepath.Join(dir, Key(0).String(), dataLogName), 0),
	)

	popErr := queue.Read(100, nil, func(items Items) (ReadOp, error) {
		if mode == ErrorModeContinue {
			require.NotEmpty(t, logger.String())
			require.NotEmpty(t, items)
		} else {
			require.Empty(t, items)
		}

		return ReadOpPop, nil
	})

	if mode == ErrorModeContinue {
		require.NoError(t, popErr)
	} else {
		require.Error(t, popErr)
	}

	require.NoError(t, queue.Close())
}

func TestAPIErrorModeDelete(t *testing.T) {
	t.Parallel()
	t.Run("abort", func(t *testing.T) {
		testAPIErrorModeDelete(t, ErrorModeAbort)
	})
	t.Run("continue", func(t *testing.T) {
		testAPIErrorModeDelete(t, ErrorModeContinue)
	})
}

func testAPIErrorModeDelete(t *testing.T, mode ErrorMode) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	logger := &LogBuffer{}
	opts := Options{
		ErrorMode: mode,
		Logger:    logger,
		BucketFunc: func(key Key) Key {
			return (key / 10) * 10
		},
	}

	queue, err := Open(dir, opts)
	require.NoError(t, err)

	require.NoError(t, queue.Push(testutils.GenItems(0, 100, 1)))

	// truncate the data log of a single
	// this will trigger a panic when working with the mmap.
	require.NoError(
		t,
		os.Truncate(filepath.Join(dir, Key(0).String(), dataLogName), 0),
	)

	ndeleted, err := queue.Delete(0, 100)
	if mode == ErrorModeContinue {
		require.NotEmpty(t, logger.String())
		require.NoError(t, err)
		require.Equal(t, 90, ndeleted)
	} else {
		require.Error(t, err)
		require.Equal(t, 0, ndeleted)
	}

	require.NoError(t, queue.Close())
}

func TestAPIBadOptions(t *testing.T) {
	t.Parallel()

	// Still create test dir to make sure it does not error out because of that:
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	opts := DefaultOptions()
	opts.BucketFunc = nil
	_, err = Open(dir, opts)
	require.Error(t, err)
}

func TestAPIPushError(t *testing.T) {
	t.Parallel()

	// Still create test dir to make sure it does not error out because of that:
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	buf := &bytes.Buffer{}
	opts := DefaultOptions()
	opts.Logger = WriterLogger(buf)
	queue, err := Open(dir, opts)
	require.NoError(t, err)

	// First push creates the
	require.NoError(t, queue.Push(testutils.GenItems(0, 10, 1)))

	// Truncating the log should trigger an error on the second push (actually a panic)
	dataPath := filepath.Join(dir, item.Key(0).String(), dataLogName)
	require.NoError(t, os.Truncate(dataPath, 0))
	require.Error(t, queue.Push(testutils.GenItems(0, 10, 1)))

	require.NoError(t, queue.Close())
}

// helper to get the number of open file descriptors for current process:
func openfds(t *testing.T) int {
	ents, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)
	return len(ents)
}

// helper to get the residual memory of the current process:
func rssBytes(t *testing.T) int64 {
	data, err := os.ReadFile("/proc/self/status")
	require.NoError(t, err)

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		split := strings.SplitN(line, ":", 2)
		if strings.TrimSpace(split[0]) != "VmRSS" {
			continue
		}

		kbs := strings.TrimSpace(strings.TrimSuffix(split[1], "kB"))
		kb, err := strconv.ParseInt(kbs, 10, 64)
		require.NoError(t, err)
		return kb * 1024
	}

	require.Fail(t, "failed to find rss")
	return 0
}

// Check if old buckets get closed when pushing a lot of data.
// Old buckets would still claim the memory maps, causing more residual memory
// usage and also increasing number of file descriptors.
func TestAPIMaxParallelBuckets(t *testing.T) {
	t.Parallel()

	// Still create test dir to make sure it does not error out because of that:
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	const N = 1000 // bucket size
	opts := DefaultOptions()
	opts.BucketFunc = func(key item.Key) item.Key {
		return (key / N) * N
	}

	// This test should fail if this is set to 0!
	opts.MaxParallelOpenBuckets = 1

	queue, err := Open(dir, opts)
	require.NoError(t, err)

	var refFds int
	var refRss int64

	// this accounts for parallel running tests.
	// the main point is to check for linear increase.
	const limit = 2.5

	for idx := 0; idx < 100; idx++ {
		if idx == 10 {
			refFds = openfds(t)
			refRss = rssBytes(t)
		}

		// regression bug: CloseUnused() did not re-add trailers for nil-buckets.
		// Also, we need to check that Len() does not re-open buckets.
		require.Equal(t, idx*N, queue.Len())

		if idx > 10 {
			// it takes a bit of time for the values to stabilize.
			fds := openfds(t)
			rss := rssBytes(t)

			if fac := float64(fds) / float64(refFds); fac > limit {
				require.Failf(
					t,
					"fd increase",
					"number of fds increases: %v at bucket #%d",
					fac,
					idx,
				)
			}

			if fac := float64(rss) / float64(refRss); fac > limit {
				require.Failf(
					t,
					"rss increase",
					"number of rss increases: %v times at bucket #%d",
					fac,
					idx,
				)
			}
		}

		require.NoError(t, queue.Push(testutils.GenItems(idx*N, idx*N+N, 1)))
	}

	require.NoError(t, queue.Close())
}

func TestAPIFixedSizeBucketFunc(t *testing.T) {
	// just to make sure that the func does not break,
	// even though the test is really stupid.
	fn := FixedSizeBucketFunc(100)
	for idx := 0; idx < 1000; idx++ {
		require.Equal(t, item.Key(idx/100)*100, fn(item.Key(idx)))
	}
}

func TestAPIDoNotCrashOnMultiBucketPop(t *testing.T) {
	mpobs := []int{0, 1, 2, 3, 5, 7, 10, 25, 50, 100, 101}
	for _, mpob := range mpobs {
		t.Run(fmt.Sprintf("%d", mpob), func(t *testing.T) {
			testAPIDoNotCrashOnMultiBucketPop(t, mpob)
		})
	}
}

func testAPIDoNotCrashOnMultiBucketPop(t *testing.T, maxParallelOpenBuckets int) {
	// Still create test dir to make sure it does not error out because of that:
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	const N = 100
	opts := DefaultOptions()
	opts.BucketFunc = func(key item.Key) item.Key {
		return (key / N) * N
	}

	opts.MaxParallelOpenBuckets = maxParallelOpenBuckets

	srcDir := filepath.Join(dir, "src")
	queue, err := Open(srcDir, opts)
	require.NoError(t, err)

	dstDir := filepath.Join(dir, "dst")
	dstQueue, err := Open(dstDir, opts)
	require.NoError(t, err)

	// Add several buckets to the queue:
	// Access all data from all buckets, so that all memory has to be touched.
	// If some memory is not mapped anymore (because the bucket was closed due
	// to the MaxParallelOpenBuckets feature) then we would find out here.
	for idx := 0; idx < N; idx++ {
		off := idx * N
		require.NoError(t, queue.Push(testutils.GenItems(off, off+N, 1)))
	}

	refFds := openfds(t)
	refRss := rssBytes(t)

	dst := make([]Item, 0, N*10)
	count := 0
	require.NoError(t, queue.Read(N*N, dst, func(items Items) (ReadOp, error) {
		for _, item := range items {
			num, err := strconv.Atoi(string(item.Blob))
			require.NoError(t, err)
			require.Equal(t, num, count)
			count++
		}

		return ReadOpPop, nil
	}))

	gotFds := openfds(t)
	gotRss := rssBytes(t)
	require.LessOrEqual(t, gotFds, refFds)
	require.True(t, float64(refRss)*1.5 > float64(gotRss))
	require.NoError(t, queue.Close())
	require.NoError(t, dstQueue.Close())
}

func TestAPIShovelMemoryUsage(t *testing.T) {
	// Still create test dir to make sure it does not error out because of that:
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	const N = 10 // bucket size
	opts := DefaultOptions()
	opts.BucketFunc = func(key item.Key) item.Key {
		return (key / N) * N
	}

	// This test should fail if this is set to 0!
	opts.MaxParallelOpenBuckets = 1

	srcDir := filepath.Join(dir, "src")
	srcQueue, err := Open(srcDir, opts)
	require.NoError(t, err)

	dstDir := filepath.Join(dir, "dst")
	dstQueue, err := Open(dstDir, opts)
	require.NoError(t, err)

	// Add a lot of mem to the srcQueue:
	for idx := 0; idx < N; idx++ {
		off := idx * N
		require.NoError(t, srcQueue.Push(testutils.GenItems(off, off+N, 1)))

		// also create the same buckets for dest queue, so that we do not take
		// the fast path that do not involve any buckets access.
		require.NoError(t, dstQueue.Push(testutils.GenItems(off, off+1, 1)))
	}

	refFds := openfds(t)
	refRss := rssBytes(t)

	count, err := srcQueue.Shovel(dstQueue)
	require.NoError(t, err)
	require.Equal(t, N*N, count)

	nowFds := openfds(t)
	nowRss := rssBytes(t)

	// Allow some fds to be extra:
	require.LessOrEqual(t, nowFds, refFds+1)

	// RSS memory usage should have not increased a lot:
	require.True(t, float64(nowRss) < float64(refRss)*1.5)

	// Just add some extra stuff to the queue since we had an
	// crash related to a broken queue after shovel:
	for idx := 0; idx < N; idx++ {
		off := idx * N
		require.NoError(t, srcQueue.Push(testutils.GenItems(off, off+N, 1)))

		// also create the same buckets for dest queue, so that we do not take
		// the fast path that do not involve any buckets access.
		require.NoError(t, dstQueue.Push(testutils.GenItems(off, off+1, 1)))
	}

	require.NoError(t, srcQueue.Close())
	require.NoError(t, dstQueue.Close())
}

func TestAPIZeroLengthPush(t *testing.T) {
	// Still create test dir to make sure it does not error out because of that:
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	queue, err := Open(dir, DefaultOptions())
	require.NoError(t, err)

	require.NoError(t, queue.Push(Items{}))
	require.NoError(t, queue.Read(1, nil, func(_ Items) (ReadOp, error) {
		require.Fail(t, "should not have been executed")
		return ReadOpPop, nil
	}))

	// Check that items after it are still reachable if there's a zero item:
	emptyItems := Items{{Key: 1, Blob: []byte{}}}
	nonEmptyItems := Items{{Key: 2, Blob: []byte("hello world")}}

	require.NoError(t, queue.Push(append(emptyItems, nonEmptyItems...)))

	var executed bool
	require.NoError(t, queue.Read(2, nil, func(items Items) (ReadOp, error) {
		require.Equal(t, emptyItems[0], items[0])
		require.Equal(t, nonEmptyItems[0], items[1])
		executed = true
		return ReadOpPop, nil
	}))

	require.True(t, executed)
	require.NoError(t, queue.Close())
}

func TestAPIZeroKeyPush(t *testing.T) {
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	queue, err := Open(dir, DefaultOptions())
	require.NoError(t, err)

	zeroKey := Items{{Key: 0, Blob: []byte{}}}
	require.NoError(t, queue.Push(zeroKey))
	require.Equal(t, 1, queue.Len())

	var executed bool
	require.NoError(t, queue.Read(1, nil, func(items Items) (ReadOp, error) {
		require.Equal(t, zeroKey, items)
		executed = true
		return ReadOpPop, nil
	}))

	require.True(t, executed)
	require.NoError(t, queue.Close())
}

func TestAPIForkBasicBeforePush(t *testing.T) {
	t.Run("push-before-full-Read", func(t *testing.T) {
		// if we push before fork, the buckets exist & are forked online.
		testAPIForkBasicBeforePush(t, true, 100, 100)
	})
	t.Run("push-after-full-Read", func(t *testing.T) {
		// if we push after fork, the buckets do not exist & are forked offline.
		testAPIForkBasicBeforePush(t, false, 100, 100)
	})
	t.Run("push-before-partial-Read", func(t *testing.T) {
		// If the bucket is fully empty, then we do the RemoveFork() offline.
		testAPIForkBasicBeforePush(t, true, 100, 99)
	})
	t.Run("push-after-partial-Read", func(t *testing.T) {
		// If the bucket is not fully empty, then we do the RemoveFork() online.
		testAPIForkBasicBeforePush(t, false, 100, 99)
	})
}

func testAPIForkBasicBeforePush(t *testing.T, pushBefore bool, pushn, popn int) {
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	exp := testutils.GenItems(0, pushn, 1)

	queue, err := Open(dir, DefaultOptions())
	require.NoError(t, err)

	// At the beginning, no forks should exist.
	require.Equal(t, []ForkName{}, queue.Forks())

	if pushBefore {
		require.NoError(t, queue.Push(exp))
	}

	fork, err := queue.Fork("fork")
	require.NoError(t, err)
	require.Equal(t, []ForkName{"fork"}, queue.Forks())

	// fork twice should not yield an error (since it's a no-op)
	_, err = queue.Fork("fork")
	require.NoError(t, err)

	// consumers should also exist, even if we start later.
	if !pushBefore {
		require.NoError(t, queue.Push(exp))
	}

	got1, err := PopCopy(fork, popn)
	require.NoError(t, err)

	got2, err := PopCopy(queue, popn)
	require.NoError(t, err)

	require.Equal(t, exp[:popn], got1)
	require.Equal(t, exp[:popn], got2)

	require.NoError(t, fork.Remove())
	require.Equal(t, []ForkName{}, queue.Forks())

	items, err := PopCopy(fork, popn)
	require.Equal(t, ErrNoSuchFork, err)
	require.Empty(t, items)

	require.NoError(t, queue.Close())
}

func TestAPIChainFork(t *testing.T) {
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	queue, err := Open(dir, DefaultOptions())
	require.NoError(t, err)

	exp := testutils.GenItems(0, 100, 1)
	require.NoError(t, queue.Push(exp))

	var forks []*Fork
	var forkNames []ForkName
	var consumers []Consumer

	for idx := 0; idx < 10; idx++ {
		forkName := ForkName(fmt.Sprintf("%d", idx))

		var c Consumer = queue
		if len(forks) > 0 {
			c = forks[len(forks)-1]
		}

		popped, err := PopCopy(c, 10)
		require.NoError(t, err)
		require.Len(t, popped, 10)
		require.Equal(t, exp[idx*10:idx*10+10], popped)

		fork, err := c.Fork(forkName)
		require.NoError(t, err)

		forkNames = append(forkNames, forkName)
		forks = append(forks, fork)
		consumers = append(consumers, c)
	}

	require.Equal(t, forkNames, queue.Forks())
	for idx, consumer := range consumers {
		require.Equal(t, 90-10*idx, consumer.Len())
	}

	for _, fork := range forks {
		require.NoError(t, fork.Remove())
	}

	require.NoError(t, queue.Close())
}

func TestAPINegativKeys(t *testing.T) {
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	queue, err := Open(dir, DefaultOptions())
	require.NoError(t, err)

	require.NoError(t, queue.Push(testutils.GenItems(-100, +100, 1)))
	require.NoError(t, queue.Push(testutils.GenItems(-100, +100, 1)))

	for idx := 0; idx < 40; idx++ {
		require.NoError(t, queue.Read(10, nil, func(items Items) (ReadOp, error) {
			for itemIdx, item := range items {
				a := (idx*10 + itemIdx - 200)
				if a <= 0 {
					// trick to balance out the shift in negative numbers:
					a -= 1
				}
				exp := a / 2
				require.Equal(t, Key(exp), item.Key)
			}
			return ReadOpPop, nil
		}))
	}

	require.NoError(t, queue.Close())
}

func TestAPIZeroLengthPayload(t *testing.T) {
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	queue, err := Open(dir, DefaultOptions())
	require.NoError(t, err)

	exp := Items{
		Item{
			Key:  123,
			Blob: []byte{},
		},
	}
	require.NoError(t, queue.Push(exp))

	got, err := PopCopy(queue, 1)
	require.NoError(t, err)
	require.Equal(t, exp, got)
}

// TODO: Better testing for negative prio keys.
// TODO: Test for bucket deletion on RemoveFork() and bucket deletion when all forks empty.
// TODO: Remove Move/Peek and make function return a boolean to indicate what to to with the
//       peeked data (remove or keep)
// TODO: Try to get rid of some of the type alias stuff, as it's rather annoying in go docs.
