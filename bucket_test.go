package timeq

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sahib/timeq/index"
	"github.com/sahib/timeq/item"
	"github.com/sahib/timeq/item/testutils"
	"github.com/stretchr/testify/require"
)

func createEmptyBucket(t *testing.T) (*bucket, string) {
	dir, err := os.MkdirTemp("", "timeq-buckettest")
	require.NoError(t, err)

	bucketDir := filepath.Join(dir, item.Key(23).String())
	bucket, err := openBucket(bucketDir, nil, DefaultOptions())
	require.NoError(t, err)

	return bucket, dir
}

// convenience function to avoid typing a lot.
func buckPop(buck *bucket, n int, dst Items, fork ForkName) (Items, int, error) {
	result := Items{}
	var popped int
	return result, popped, buck.Read(n, dst, fork, func(items Items) (ReadOp, error) {
		result = append(result, items.Copy()...)
		popped += len(items)
		return ReadOpPop, nil
	})
}

func buckPeek(buck *bucket, n int, dst Items, fork ForkName) (Items, int, error) {
	result := Items{}
	var peeked int
	return result, peeked, buck.Read(n, dst, fork, func(items Items) (ReadOp, error) {
		result = append(result, items.Copy()...)
		peeked += len(items)
		return ReadOpPeek, nil
	})
}

func buckMove(buck, dstBuck *bucket, n int, dst Items, fork ForkName) (Items, int, error) {
	result := Items{}
	var moved int
	return result, moved, buck.Read(n, dst, fork, func(items Items) (ReadOp, error) {
		result = append(result, items.Copy()...)
		moved += len(items)
		return ReadOpPop, dstBuck.Push(items, true, fork)
	})
}

func withEmptyBucket(t *testing.T, fn func(b *bucket)) {
	t.Parallel()

	buck, dir := createEmptyBucket(t)
	defer os.RemoveAll(dir)
	fn(buck)
	require.NoError(t, buck.Close())
}

func TestBucketOpenEmpty(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		require.True(t, buck.Empty(""))
		require.Equal(t, 0, buck.Len(""))
	})
}

func TestBucketPushEmpty(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		require.NoError(t, buck.Push(nil, true, ""))
	})
}

func TestBucketPopZero(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		dst := testutils.GenItems(0, 10, 1)[:0]
		gotItems, nPopped, err := buckPop(buck, 0, dst, "")
		require.NoError(t, err)
		require.Equal(t, dst, gotItems)
		require.Equal(t, 0, nPopped)
	})
}

func TestBucketPopEmpty(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		dst := testutils.GenItems(0, 10, 1)[:0]
		gotItems, nPopped, err := buckPop(buck, 100, dst, "")
		require.NoError(t, err)
		require.Equal(t, 0, nPopped)
		require.Equal(t, dst, gotItems)
	})
}

func TestBucketPushPop(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		expItems := testutils.GenItems(0, 10, 1)
		require.NoError(t, buck.Push(expItems, true, ""))
		gotItems, nPopped, err := buckPop(buck, len(expItems), nil, "")
		require.NoError(t, err)
		require.Equal(t, expItems, gotItems)
		require.Equal(t, len(expItems), nPopped)
	})
}

func TestBucketPushPopReverse(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		expItems := testutils.GenItems(10, 0, -1)
		require.NoError(t, buck.Push(expItems, true, ""))
		gotItems, nPopped, err := buckPop(buck, len(expItems), nil, "")
		require.NoError(t, err)
		require.Equal(t, expItems, gotItems)
		require.Equal(t, len(expItems), nPopped)
	})
}

func TestBucketPushPopSorted(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		push1 := testutils.GenItems(0, 10, 1)
		push2 := testutils.GenItems(11, 20, 1)
		expItems := append(push1, push2...)
		require.NoError(t, buck.Push(push2, true, ""))
		require.NoError(t, buck.Push(push1, true, ""))
		gotItems, nPopped, err := buckPop(buck, len(push1)+len(push2), nil, "")
		require.NoError(t, err)
		require.Equal(t, len(push1)+len(push2), nPopped)
		require.Equal(t, expItems, gotItems)
	})
}

func TestBucketPushPopZip(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		push1 := testutils.GenItems(0, 20, 2)
		push2 := testutils.GenItems(1, 20, 2)
		require.NoError(t, buck.Push(push2, true, ""))
		require.NoError(t, buck.Push(push1, true, ""))
		gotItems, nPopped, err := buckPop(buck, len(push1)+len(push2), nil, "")
		require.NoError(t, err)

		for idx := 0; idx < 20; idx++ {
			require.Equal(t, testutils.ItemFromIndex(idx), gotItems[idx])
		}

		require.Equal(t, len(push1)+len(push2), nPopped)
	})
}

func TestBucketPopSeveral(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		expItems := testutils.GenItems(0, 10, 1)
		require.NoError(t, buck.Push(expItems, true, ""))
		gotItems1, nPopped1, err := buckPop(buck, 5, nil, "")
		require.NoError(t, err)
		gotItems2, nPopped2, err := buckPop(buck, 5, nil, "")
		require.NoError(t, err)

		require.Equal(t, 5, nPopped1)
		require.Equal(t, 5, nPopped2)
		require.Equal(t, expItems, append(gotItems1, gotItems2...))
	})
}

func TestBucketPushPopSeveral(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		push1 := testutils.GenItems(0, 20, 2)
		push2 := testutils.GenItems(1, 20, 2)
		require.NoError(t, buck.Push(push2, true, ""))
		require.NoError(t, buck.Push(push1, true, ""))
		gotItems1, nPopped1, err := buckPop(buck, 10, nil, "")
		require.NoError(t, err)
		gotItems2, nPopped2, err := buckPop(buck, 10, nil, "")
		require.NoError(t, err)

		require.Equal(t, 10, nPopped1)
		require.Equal(t, 10, nPopped2)

		gotItems := append(gotItems1, gotItems2...)
		for idx := 0; idx < 20; idx++ {
			require.Equal(t, testutils.ItemFromIndex(idx), gotItems[idx])
		}
	})
}

func TestBucketPopLarge(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		expItems := testutils.GenItems(0, 10, 1)
		require.NoError(t, buck.Push(expItems, true, ""))
		gotItems, nPopped, err := buckPop(buck, 20, nil, "")
		require.NoError(t, err)
		require.Equal(t, len(expItems), nPopped)
		require.Equal(t, expItems, gotItems)

		gotItems, nPopped, err = buckPop(buck, 20, nil, "")
		require.NoError(t, err)
		require.Equal(t, 0, nPopped)
		require.Len(t, gotItems, 0)
	})
}

func TestBucketLen(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		require.Equal(t, 0, buck.Len(""))
		require.True(t, buck.Empty(""))

		expItems := testutils.GenItems(0, 10, 1)
		require.NoError(t, buck.Push(expItems, true, ""))
		require.Equal(t, 10, buck.Len(""))
		require.False(t, buck.Empty(""))

		_, _, err := buckPop(buck, 5, nil, "")
		require.NoError(t, err)
		require.Equal(t, 5, buck.Len(""))
		require.False(t, buck.Empty(""))

		_, _, err = buckPop(buck, 5, nil, "")
		require.NoError(t, err)
		require.True(t, buck.Empty(""))
		require.Equal(t, 0, buck.Len(""))
	})
}

// TODO:
//   - Test for deleting something in the middle of a loc.
//   - Delete of multiple, overlapping locs.
//   - Buckets test:
//   - Delete all (int_min, int_max)
//   - Delete first one only.
//   - Delete last only.
//   - One-off around a bucket
func TestBucketDelete(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		require.Equal(t, 0, buck.Len(""))
		require.True(t, buck.Empty(""))

		expItems := testutils.GenItems(0, 100, 1)
		require.NoError(t, buck.Push(expItems, true, ""))
		require.Equal(t, 100, buck.Len(""))

		deleted, err := buck.Delete("", 0, 50)
		require.NoError(t, err)
		require.Equal(t, 51, deleted)
		require.False(t, buck.Empty(""))

		existing, npeeked, err := buckPeek(buck, 100, nil, "")
		require.NoError(t, err)
		require.Equal(t, 49, npeeked)
		require.Equal(t, expItems[51:], existing)

		deleted, err = buck.Delete("", 0, 100)
		require.NoError(t, err)
		require.Equal(t, 49, deleted)
		require.True(t, buck.Empty(""))

		// to < from
		_, err = buck.Delete("", 100, 99)
		require.Error(t, err)
	})
}

func TestBucketDeleteLeftAndRight(t *testing.T) {
	tcs := []struct {
		Name     string
		From, To item.Key
	}{
		{
			Name: "full_inclusive",
			From: 0,
			To:   100,
		}, {
			Name: "full_high_to",
			From: 0,
			To:   1000,
		}, {
			Name: "full_low_from",
			From: -100,
			To:   100,
		}, {
			Name: "full_both",
			From: -100,
			To:   100,
		}, {
			Name: "partial_one_item",
			From: 50,
			To:   50,
		}, {
			Name: "partial_two_items",
			From: 50,
			To:   51,
		}, {
			Name: "leftmost",
			From: 0,
			To:   0,
		}, {
			Name: "rightmost",
			From: 99,
			To:   99,
		}, {
			Name: "right_only",
			From: 0,
			To:   10,
		},
	}

	for _, tc := range tcs {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			withEmptyBucket(t, func(buck *bucket) {
				buck.key = 0 // fake sets it with 23; we need 0 here.

				require.Equal(t, 0, buck.Len(""))
				require.True(t, buck.Empty(""))

				expItems := testutils.GenItems(0, 100, 1)
				require.NoError(t, buck.Push(expItems, true, ""))
				require.Equal(t, 100, buck.Len(""))

				clampedTo := tc.To
				if tc.To > 99 {
					clampedTo = 99
				} else if tc.To < 0 {
					clampedTo = 0
				}

				clampedFrom := tc.From
				if tc.From < 0 {
					clampedFrom = 0
				} else if tc.From > 99 {
					clampedFrom = 99
				}

				ndeletedExp := clampedTo - clampedFrom + 1

				ndeleted, err := buck.Delete("", tc.From, tc.To)
				require.NoError(t, err)
				require.Equal(t, ndeletedExp, item.Key(ndeleted))

				got, npeeked, err := buckPeek(buck, 100, item.Items{}, "")
				require.Equal(t, 100-ndeleted, npeeked)
				require.NoError(t, err)
				require.Equal(
					t,
					append(
						expItems[:clampedFrom],
						expItems[clampedTo+1:]...,
					),
					got,
				)

				if ndeleted == 100 {
					require.True(t, buck.Empty(""))
				} else {
					require.False(t, buck.Empty(""))
				}
			})
		})
	}
}

func TestBucketDeleteLowerThanReopen(t *testing.T) {
	buck, dir := createEmptyBucket(t)
	defer os.RemoveAll(dir)

	require.Equal(t, 0, buck.Len(""))
	require.True(t, buck.Empty(""))

	expItems := testutils.GenItems(0, 100, 1)
	require.NoError(t, buck.Push(expItems, true, ""))
	require.Equal(t, 100, buck.Len(""))

	deleted, err := buck.Delete("", 0, 50)
	require.NoError(t, err)
	require.Equal(t, 51, deleted)
	require.False(t, buck.Empty(""))

	// Re-open the bucket:
	require.NoError(t, buck.Close())
	buck, err = openBucket(buck.dir, nil, buck.opts)
	require.NoError(t, err)

	// Pop should now see the previous 100:
	items, npopped, err := buckPop(buck, 100, nil, "")
	require.Equal(t, 49, npopped)
	require.Equal(t, expItems[51:], items)
	require.NoError(t, err)
	require.NoError(t, buck.Close())
}

func TestBucketPushDuplicates(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		const pushes = 100
		expItems := testutils.GenItems(0, 10, 1)
		for idx := 0; idx < pushes; idx++ {
			require.NoError(t, buck.Push(expItems, true, ""))
			require.Equal(t, (idx+1)*len(expItems), buck.Len(""))
		}

		buckLen := buck.Len("")
		gotItems, popped, err := buckPop(buck, buckLen, nil, "")
		require.NoError(t, err)
		require.Equal(t, buckLen, popped)
		require.Equal(t, buckLen, len(gotItems))
		require.True(t, slices.IsSortedFunc(gotItems, func(i, j item.Item) int {
			return int(i.Key - j.Key)
		}))

		for key := 0; key < len(expItems); key++ {
			for idx := 0; idx < pushes; idx++ {
				it := gotItems[key*pushes+idx]
				require.Equal(t, item.Key(key), it.Key)
			}
		}
	})
}

func TestBucketPeek(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		const N = 100
		exp := testutils.GenItems(0, N, 1)
		require.NoError(t, buck.Push(exp, true, ""))

		// peek should not delete something, so check it's idempotent.
		for idx := 0; idx < 2; idx++ {
			got, npeeked, err := buckPeek(buck, N, nil, "")
			require.NoError(t, err)
			require.Equal(t, N, npeeked)
			require.Equal(t, exp, got)
		}

		// A consequent pop() should yield the same result:
		got, npeeked, err := buckPop(buck, N, nil, "")
		require.NoError(t, err)
		require.Equal(t, N, npeeked)
		require.Equal(t, exp, got)
	})
}

func TestBucketMove(t *testing.T) {
	t.Parallel()

	srcBuck, srcDir := createEmptyBucket(t)
	dstBuck, dstDir := createEmptyBucket(t)
	defer os.RemoveAll(srcDir)
	defer os.RemoveAll(dstDir)

	const N = 100
	exp := testutils.GenItems(0, N, 1)
	require.NoError(t, srcBuck.Push(exp, true, ""))

	// move the first elem:
	moved, nshoveled, err := buckMove(srcBuck, dstBuck, 1, nil, "")
	require.NoError(t, err)
	require.Equal(t, exp[0], moved[0])
	require.Equal(t, 1, nshoveled)

	// move the rest:
	moved, nshoveled, err = buckMove(srcBuck, dstBuck, N-1, nil, "")
	require.NoError(t, err)
	require.Equal(t, exp[1:], moved)
	require.Equal(t, N-1, nshoveled)

	require.NoError(t, srcBuck.Close())
	require.NoError(t, dstBuck.Close())
}

func TestBucketRegenWith(t *testing.T) {
	tcs := []struct {
		Name      string
		DamageFn  func(path string) error
		IsDamaged bool
	}{{
		Name:      "removed_index",
		DamageFn:  os.Remove,
		IsDamaged: true,
	}, {
		Name:      "empty_index",
		DamageFn:  func(path string) error { return os.Truncate(path, 0) },
		IsDamaged: true,
	}, {
		Name:      "bad_permissions",
		DamageFn:  func(path string) error { return os.Chmod(path, 0300) },
		IsDamaged: true,
	}, {
		Name:      "broken_index",
		DamageFn:  func(path string) error { return os.Truncate(path, index.LocationSize-1) },
		IsDamaged: true,
	}, {
		Name:      "not_damaged",
		DamageFn:  func(path string) error { return nil },
		IsDamaged: false,
	}}

	for _, tc := range tcs {
		t.Run(tc.Name, func(t *testing.T) {
			t.Run("noreopen", func(t *testing.T) {
				testBucketRegenWith(t, tc.IsDamaged, false, tc.DamageFn)
			})
			t.Run("reopen", func(t *testing.T) {
				testBucketRegenWith(t, tc.IsDamaged, true, tc.DamageFn)
			})
		})
	}
}

func testBucketRegenWith(t *testing.T, isDamaged bool, reopen bool, damageFn func(path string) error) {
	buck, dir := createEmptyBucket(t)
	defer os.RemoveAll(dir)

	const N = 100
	exp1 := testutils.GenItems(0, N, 2)
	exp2 := testutils.GenItems(1, N, 2)
	exp := append(exp1, exp2...)
	slices.SortFunc(exp, func(i, j item.Item) int {
		return int(i.Key - j.Key)
	})

	require.NoError(t, buck.Push(exp1, true, ""))
	require.NoError(t, buck.Push(exp2, true, ""))
	require.NoError(t, buck.Close())

	bucketDir := filepath.Join(dir, buck.Key().String())
	idxPath := filepath.Join(bucketDir, "idx.log")
	require.NoError(t, damageFn(idxPath))

	// Re-opening the bucket should regenerate the index
	// from the value log contents:
	var err error
	var logBuffer bytes.Buffer
	opts := DefaultOptions()
	opts.Logger = &writerLogger{
		w: &logBuffer,
	}

	// This should trigger the reindex:
	buck, err = openBucket(bucketDir, nil, opts)
	require.NoError(t, err)

	if reopen {
		// on reindex we store the index in memory.
		// make sure we do not make mistakes during writing.
		require.NoError(t, buck.Close())
		buck, err = openBucket(bucketDir, nil, opts)
		require.NoError(t, err)
	}

	// The idx file should already exist again:
	_, err = os.Stat(idxPath)
	require.NoError(t, err)

	// Let's check it gets created correctly:
	got, npopped, err := buckPop(buck, N, nil, "")
	require.NoError(t, err)
	require.Equal(t, N, npopped)
	require.Equal(t, exp, got)

	if isDamaged {
		require.NotEmpty(t, logBuffer.String())
	} else {
		require.Empty(t, logBuffer.String())
	}
}

// Test if openBucket() notices that we're wasting space and cleans up afterwards.
func TestBucketReinitOnEmpty(t *testing.T) {
	t.Run("no-close-after-reinit", func(t *testing.T) {
		testBucketReinitOnEmpty(t, false)
	})
	t.Run("close-after-reinit", func(t *testing.T) {
		testBucketReinitOnEmpty(t, true)
	})
}

func testBucketReinitOnEmpty(t *testing.T, closeAfterReinit bool) {
	t.Parallel()

	buck, dir := createEmptyBucket(t)
	defer os.RemoveAll(dir)

	exp := testutils.GenItems(0, 100, 1)
	require.NoError(t, buck.Push(exp, true, ""))
	got, npopped, err := buckPop(buck, 100, nil, "")
	require.NoError(t, err)
	require.Equal(t, exp, got)
	require.Equal(t, 100, npopped)
	require.NoError(t, buck.Close())

	// re-open the same bucket - it's empty, but still has data laying around.
	// it should still be operational like before.
	bucketDir := filepath.Join(dir, item.Key(23).String())
	newBuck, err := openBucket(bucketDir, nil, DefaultOptions())

	if closeAfterReinit {
		require.NoError(t, err)
		require.NoError(t, newBuck.Close())
		newBuck, err = openBucket(bucketDir, nil, DefaultOptions())
		require.NoError(t, err)
	}

	require.NoError(t, newBuck.Push(exp, true, ""))
	got, npopped, err = buckPop(newBuck, 100, nil, "")
	require.NoError(t, err)
	require.Equal(t, exp, got)
	require.Equal(t, 100, npopped)

	require.NoError(t, newBuck.Close())
}

func TestBucketForkNameValidate(t *testing.T) {
	require.NoError(t, ForkName("hello-world").Validate())
	require.NoError(t, ForkName("HELLO_WORLD").Validate())
	require.NoError(t, ForkName("0").Validate())
	require.NoError(t, ForkName("fOrK999").Validate())
	require.NoError(t, ForkName("_____").Validate())
	require.NoError(t, ForkName("_-_-_").Validate())

	require.Error(t, ForkName("").Validate())
	require.Error(t, ForkName("space here").Validate())
	require.Error(t, ForkName("space-at-the-end ").Validate())
	require.Error(t, ForkName("fork/sub").Validate())
	require.Error(t, ForkName("huh?").Validate())
}

func TestBucketForkInvalid(t *testing.T) {
	withEmptyBucket(t, func(buck *bucket) {
		require.Error(t, buck.Fork("not-existing", "fork"))

		// forking twice should not yield an error the second time:
		require.NoError(t, buck.Fork("", "fork"))
		require.NoError(t, buck.Fork("", "fork"))
	})
}
