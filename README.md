# ``timeq``

[![GoDoc](https://godoc.org/github.com/sahib/timeq?status.svg)](https://godoc.org/github.com/sahib/timeq)
![Build status](https://github.com/sahib/timeq/actions/workflows/go.yml/badge.svg)

A persistent priority queue in Go.

> [!WARNING]
> This is still in active development. Not every goal described below was reached yet.

## Features

- Clean and well test code base based on Go 1.21.
- Configurable durability behavior.
- High (enough) throughput.
- Small memory footprint.
- Simple interface with classic `Push()` and `Pop()` and only
  few other functions.

This implementation does currently not seek to be generally useful but seeks to
be high performant and understandable at the same time. We make the following
assumption for optimal performance:

- Your OS supports `mmap()` and `mremap()` (tested on Linux)
- Seeking is not too expensive (i.e. no rotating disks)
- The priority key ideally increases without much duplicates (like timestamps, see [FAQ](#FAQ)).
- You push and pop your data in, ideally big, batches.
- File storage is not a primary concern (i.e. no compression implemented).
- The underlying storage has a low risk for write errors or bit flips.
- You trust your data safety to some random dude on the internet (don't we all?).

If some of those assumptions do not fit your usecase and you still managed to make it work,
I would be happy for some feedback or even pull requests to improve the general usability.

## Usecase

My primary usecase was a embedded linux device that has different services that generate
a stream of data that needs to be send to the cloud. For this the data was required to be
in ascending order (sorted by time) and also needed to be buffered with tight memory boundaries.

A previous attempt with ``sqlite3`` did work well but was much slower than it had to be (also
due to the heavy cost of ``cgo``). This motivated to write this queue implementation.

## Usage

To download the library, just do this in your project:

```bash
$ go get github.com/sahib/timeq@latest
```

We also ship a very minimal command-line client that can be used for experiments.
You can install it like this:

```bash
$ go install github.com/sahib/timeq/cmd@latest
```

## Benchmarks

The included benchmark pushes 2000 items with a payload of 40 byte per operation.

```
$ go test -bench=. -run=xxx
goos: linux
goarch: amd64
pkg: github.com/sahib/timeq
cpu: 12th Gen Intel(R) Core(TM) i7-1270P
BenchmarkPopSyncNone-16      	  35924	    33738 ns/op	    240 B/op	      5 allocs/op
BenchmarkPopSyncData-16      	  35286	    33938 ns/op	    240 B/op	      5 allocs/op
BenchmarkPopSyncIndex-16     	  34030	    34003 ns/op	    240 B/op	      5 allocs/op
BenchmarkPopSyncFull-16      	  35170	    33592 ns/op	    240 B/op	      5 allocs/op
BenchmarkPushSyncNone-16     	  20336	    56867 ns/op	     72 B/op	      2 allocs/op
BenchmarkPushSyncData-16     	  20630	    58613 ns/op	     72 B/op	      2 allocs/op
BenchmarkPushSyncIndex-16    	  20684	    58782 ns/op	     72 B/op	      2 allocs/op
BenchmarkPushSyncFull-16     	  19994	    59491 ns/op	     72 B/op	      2 allocs/op
```

## Design

* All data is divided into buckets by a user-defined function.
* Each bucket is it's own priority queue, responsible for a part of the key space.
* A push to a bucket writes the batch of data to a memory-mapped
  Write-Ahead-Log (WAL) file on disk. The location of the batch is stored in an
  in-memory index and to a index WAL.
* On pop we select the bucket with the lowest key first and ask the index to give
  us the location of the lowest batch. Once done the index is updated to mark the
  items as popped. The data stays intact in the data WAL.
* Once a bucket was completely drained it is removed from disk to retain space.

Since the index is quite small (only one entry per batch) we can fit it in memory.
On the initial load all bucket indexes are loaded, but no memory is mapped yet.

## FAQ:

### Can timeq be also used with non-time based keys?

There are no notable place where the key of an item is actually assumed to be
timestamp, except for the default bucket func (which can be configured). If you
find a good way to sort your data into buckets you should be good to go. Keep
in mind that timestamps were the idea behind the original design, so your
mileage may vary - always benchmark your individual usecase. You can modify one
of the existing benchmarks to test your assumptions.

### Why should I care about buckets?

Most improtantly: Only the buckets are loaded that are actually used.
This let's the memory requirement of `timeq` be extremely small.

There are also some other reasons:

* If one bucket becomes corrupt for some reason, you loose only the data in this bucket.
* Buckets cannot grow over 4GB due to the offsets being 32-bit values.
* On ``Shovel()`` we can cheaply move buckets if they do not exist in the destination.

### Can I store more than one value per key?

Yes, you can. The index may store more than one batch per key. There is a
slight allocation overhead though on ``Queue.Push()`` though. Since ``timeq``
was mostly optimized for mostly-unique keys (i.e. timestamps) you might see
better performance with less duplicates.

If you want to use priority keys that are in a very narrow range (thus many
duplicates) then you can think about spreading the range a bit wider.

For example: You have priority keys from zero to ten for the tasks in your job
queue. Instead of using zero to ten as keys, you can add the job-id to the key
and shift the priority: ``(prio << 32) | jobID``.

### How failsafe is ``timeq``?

Time will tell. I intend to use it on a big fleet of embedded devices in the
field. Design wise, damaged index files can be regenerated from the data log.
There's no error correction code applied in the data log and no checksums are
currently written. If you need this, I'm happy if a PR comes in that enables it
as option.

For durability, the design is build to survice crashes without data loss (Push,
Pop) but, in some cases, with duplicated data (Shovel). This assumes a filesystem
with full journaling (``data=journal`` for ext4) or some other filesystem that gives
your similar guarantees. At this point, this was not really tested in the wild yet.
My recommendation is designing your application logic in a way that allows duplicate
items to be handled from the queue.

The test suite is currently roughly as big as the codebase. The best protection
against bugs is a small code base though, so that's not too impressive yet.
We're working on improving the testsuite (which is a never ending task). The
actual coverage is higher than what is shown in `make test` since most
higher-level tests also test lower-level functionality. Additionally we have a
bunch of benchmarks and fuzzing tests.

## License

Source code is available under the MIT [License](/LICENSE).

## Contact

Chris Pahl [@sahib](https://github.com/sahib)

## TODO List

- [x] Add fuzzing test for Push/Pop
- [x] Use a configurable logger for warnings
- [x] We crash currently when running out of space.
- [x] Figure out error handling. If a bucket is unreadable, fail or continue?
- [ ] Improve test coverage / extend test suite
- [ ] Profile and optimize a bit more, if possible.
