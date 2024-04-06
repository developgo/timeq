//go:build slow
// +build slow

package timeq

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func getSizeOfDir(t *testing.T, root string) (size int64) {
	require.NoError(t, filepath.Walk(root, func(_ string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		size += info.Size()
		return nil
	}))
	return
}

// Check that we can create value logs over 4G in size.
func TestAPI4GLog(t *testing.T) {
	dir, err := os.MkdirTemp("", "timeq-apitest")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	opts := DefaultOptions()
	opts.BucketFunc = ShiftBucketFunc(5 * 1024 * 1024 * 1024)
	queue, err := Open(dir, opts)
	require.NoError(t, err)

	const N = 1000

	var items Items
	for idx := 0; idx < N; idx++ {
		items = append(items, Item{
			Key:  Key(idx),
			Blob: make([]byte, 16*1024),
		})
	}

	const FourGB = 4 * 1024 * 1024 * 1024
	var expected int
	for getSizeOfDir(t, dir) <= FourGB+(1*1024*1024) {
		require.NoError(t, queue.Push(items))
		expected += len(items)
	}

	var got int
	var dst = make(Items, 0, N)
	for queue.Len() > 0 {
		require.NoError(t, queue.Read(N, dst, func(items Items) (ReadOp, error) {
			got += len(items)
			return ReadOpPop, nil
		}))
	}

	require.Equal(t, got, expected)
	require.NoError(t, queue.Close())
}
