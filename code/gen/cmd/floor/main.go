// Command floor measures the physical floor for 1BRC on this machine: how fast the file
// can be read at all, and how fast the cheapest conceivable scans over it run.
//
//	go run ./cmd/floor -mode read -in $ASSETS/measurements-1b.txt -rows 1000000000
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// fNoCache is darwin's F_NOCACHE. Setting it to 1 tells the kernel not to keep this
// descriptor's pages in the unified buffer cache, which is the only way to measure an
// uncached read here: `purge` needs sudo and this loop runs unattended.
const fNoCache = 48

func main() {
	in := flag.String("in", "", "measurements file to scan (required)")
	mode := flag.String("mode", "read", "read (I/O only) | count (bytes.Count newlines) | scan (bufio.Scanner lines) | mmap (count newlines over a mapping, no copy)")
	bsKiB := flag.Int("bs", 1024, "read chunk size in KiB")
	nocache := flag.Bool("nocache", false, "set F_NOCACHE so the read bypasses the page cache")
	workers := flag.Int("workers", 1, "parallel readers over disjoint byte ranges (read/count only)")
	rows := flag.Int64("rows", 0, "expected row count, for the ns/row column (0 = use the counted lines)")
	flag.Parse()

	if *in == "" {
		log.Fatal("-in is required")
	}
	n, lines, elapsed, err := run(*in, *mode, *bsKiB*1024, *nocache, *workers)
	if err != nil {
		log.Fatal(err)
	}

	perRow := *rows
	if perRow == 0 {
		perRow = lines
	}
	gbps := float64(n) / elapsed.Seconds() / 1e9
	out := fmt.Sprintf("mode=%s workers=%d nocache=%v bs=%dKiB bytes=%d lines=%d elapsed=%s rate=%.2f GB/s",
		*mode, *workers, *nocache, *bsKiB, n, lines, elapsed.Round(time.Millisecond), gbps)
	if perRow > 0 {
		out += fmt.Sprintf(" per_row=%.2f ns", float64(elapsed.Nanoseconds())/float64(perRow))
	}
	fmt.Println(out)
}

// run scans path in the given mode and reports bytes read, newlines seen and wall time.
// count mode is exact across chunk boundaries because a newline tally does not span them;
// read mode reports lines=0 since it never looks at the bytes.
func run(path, mode string, bs int, nocache bool, workers int) (n, lines int64, elapsed time.Duration, err error) {
	if workers < 1 {
		return 0, 0, 0, fmt.Errorf("-workers must be >= 1, got %d", workers)
	}
	if workers > 1 && mode == "scan" {
		return 0, 0, 0, fmt.Errorf("-mode scan is single-threaded by construction; -workers %d is meaningless", workers)
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, err
	}
	defer f.Close()

	if nocache {
		if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), uintptr(fNoCache), 1); e != 0 {
			return 0, 0, 0, fmt.Errorf("fcntl F_NOCACHE: %w", e)
		}
	}

	info, err := f.Stat()
	if err != nil {
		return 0, 0, 0, err
	}
	size := info.Size()

	start := time.Now()
	switch mode {
	case "read", "count":
		if workers > 1 {
			n, lines, err = parallelScan(f, size, bs, workers, mode == "count")
			if err != nil {
				return 0, 0, 0, err
			}
			break
		}
		buf := make([]byte, bs)
		for {
			r, rerr := f.Read(buf)
			n += int64(r)
			if mode == "count" {
				lines += int64(bytes.Count(buf[:r], []byte{'\n'}))
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				return 0, 0, 0, rerr
			}
		}
	case "mmap":
		n, lines, err = mmapCount(f, size, workers)
		if err != nil {
			return 0, 0, 0, err
		}
	case "scan":
		sc := bufio.NewScanner(bufio.NewReaderSize(f, bs))
		sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
		for sc.Scan() {
			n += int64(len(sc.Bytes())) + 1
			lines++
		}
		if err := sc.Err(); err != nil {
			return 0, 0, 0, err
		}
	default:
		return 0, 0, 0, fmt.Errorf("unknown -mode %q", mode)
	}
	return n, lines, time.Since(start), nil
}

// parallelScan splits the file into one contiguous range per worker and preads each range
// independently. Ranges are cut on raw byte offsets, not line boundaries: this measures
// bandwidth, so a range that starts mid-line is fine as long as every byte is read once.
func parallelScan(f *os.File, size int64, bs, workers int, count bool) (int64, int64, error) {
	span := (size + int64(workers) - 1) / int64(workers)
	var (
		wg       sync.WaitGroup
		bytesN   atomic.Int64
		linesN   atomic.Int64
		firstErr atomic.Value
	)
	for w := 0; w < workers; w++ {
		lo := int64(w) * span
		if lo >= size {
			break
		}
		hi := min(lo+span, size)
		wg.Add(1)
		go func(lo, hi int64) {
			defer wg.Done()
			buf := make([]byte, bs)
			var localBytes, localLines int64
			for off := lo; off < hi; {
				want := min(int64(bs), hi-off)
				r, err := f.ReadAt(buf[:want], off)
				localBytes += int64(r)
				if count {
					localLines += int64(bytes.Count(buf[:r], []byte{'\n'}))
				}
				off += int64(r)
				if err != nil && err != io.EOF {
					firstErr.CompareAndSwap(nil, err)
					return
				}
				if r == 0 {
					break
				}
			}
			bytesN.Add(localBytes)
			linesN.Add(localLines)
		}(lo, hi)
	}
	wg.Wait()
	if err, ok := firstErr.Load().(error); ok {
		return 0, 0, err
	}
	return bytesN.Load(), linesN.Load(), nil
}

// mmapCount maps the whole file and counts newlines over disjoint slices of the mapping.
// Unlike read mode this never copies into user memory, so it is the bandwidth ceiling a
// real solution can actually reach: page faults, not read(), are the cost.
func mmapCount(f *os.File, size int64, workers int) (int64, int64, error) {
	if size == 0 {
		return 0, 0, nil
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return 0, 0, fmt.Errorf("mmap %d bytes: %w", size, err)
	}
	defer syscall.Munmap(data)

	span := (size + int64(workers) - 1) / int64(workers)
	var wg sync.WaitGroup
	linesN := make([]int64, workers)
	for w := 0; w < workers; w++ {
		lo := int64(w) * span
		if lo >= size {
			break
		}
		hi := min(lo+span, size)
		wg.Add(1)
		go func(w int, lo, hi int64) {
			defer wg.Done()
			linesN[w] = int64(bytes.Count(data[lo:hi], []byte{'\n'}))
		}(w, lo, hi)
	}
	wg.Wait()
	var lines int64
	for _, c := range linesN {
		lines += c
	}
	return size, lines, nil
}
