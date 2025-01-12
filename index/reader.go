package index

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sahib/timeq/item"
)

const TrailerSize = 4

// LocationSize is the physical storage of a single item
// (8 for the key, 8 for the wal offset, 4 for the len)
const LocationSize = 8 + 8 + 4 + TrailerSize

type Trailer struct {
	TotalEntries item.Off
}

// Reader gives access to a single index on disk
type Reader struct {
	r      io.Reader
	err    error
	locBuf [LocationSize]byte
}

func NewReader(r io.Reader) *Reader {
	return &Reader{
		// Reduce number of syscalls needed:
		r: bufio.NewReaderSize(r, 4*1024),
	}
}

func (fi *Reader) Next(loc *item.Location) bool {
	if _, err := io.ReadFull(fi.r, fi.locBuf[:]); err != nil {
		if err != io.EOF {
			fi.err = err
		}

		return false
	}

	loc.Key = item.Key(binary.BigEndian.Uint64(fi.locBuf[:8]))
	loc.Off = item.Off(binary.BigEndian.Uint64(fi.locBuf[8:]))
	loc.Len = item.Off(binary.BigEndian.Uint32(fi.locBuf[16:]))
	// NOTE: trailer with size / len is ignored here. See ReadTrailer()
	return true
}

func (fi *Reader) Err() error {
	return fi.err
}

func ReadTrailers(dir string, fn func(consumerName string, trailer Trailer)) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, ent := range ents {
		name := ent.Name()
		if !strings.HasSuffix(name, "idx.log") {
			continue
		}

		path := filepath.Join(dir, name)
		trailer, err := ReadTrailer(path)
		if err != nil {
			return err
		}

		consumerName := strings.TrimSuffix(name, "idx.log")
		consumerName = strings.TrimSuffix(consumerName, ".")
		fn(consumerName, trailer)
	}

	return nil
}

// ReadTrailer reads the trailer of the index log.
// It contains the number of entries in the index.
func ReadTrailer(path string) (Trailer, error) {
	fd, err := os.Open(path)
	if err != nil {
		return Trailer{}, err
	}
	defer fd.Close()

	info, err := fd.Stat()
	if err != nil {
		return Trailer{}, err
	}

	if info.Size() < LocationSize {
		return Trailer{TotalEntries: 0}, nil
	}

	if _, err := fd.Seek(-TrailerSize, io.SeekEnd); err != nil {
		return Trailer{}, err
	}

	buf := make([]byte, TrailerSize)
	if _, err := io.ReadFull(fd, buf); err != nil {
		return Trailer{}, nil
	}

	totalEntries := item.Off(binary.BigEndian.Uint32(buf))
	return Trailer{TotalEntries: totalEntries}, nil
}
