package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	gen "github.com/noelruault/research/1brc/code/gen"
)

// fNoCache is darwin's F_NOCACHE. 02-baseline.md measured 15 uncached parallel preads reading the 13.8 GB file in 754 ms against 1.126 s page-cached, because the file is 53.5% of RAM and the head is evicted while the tail is read.
const fNoCache = 48

// madvWillNeed is darwin's MADV_WILLNEED. syscall has no Madvise on darwin, so the raw call it is.
const madvWillNeed = 3

// phasesOn splits each worker's wall clock into blocked-in-read against folding, so the ledger's queue item 3 ("11.5 of 15 cores busy — I/O wait, the merge, or shard skew?") is answered by measurement.
// Off by default because the default binary is the one being timed: 13.8 GB at 4 MiB is ~3,290 chunks, so the two clock reads per chunk are ~0.02% of 1.6 s, but an unpriced change to the timed path is not worth the convenience.
var phasesOn bool

var (
	phaseRead   atomic.Int64
	phaseFold   atomic.Int64
	phaseChunks atomic.Int64
)

// reportPhases writes the split to stderr. workerWall is one entry per worker, so the spread across it IS the shard skew, and read+fold against the max tells how much of the wall clock a fill-ahead worker (H-14) could hide.
func reportPhases(w io.Writer, workerWall []time.Duration, merge time.Duration) {
	wall := append([]time.Duration(nil), workerWall...)
	sort.Slice(wall, func(i, j int) bool { return wall[i] < wall[j] })
	var sum time.Duration
	for _, d := range wall {
		sum += d
	}
	read, fold := time.Duration(phaseRead.Load()), time.Duration(phaseFold.Load())
	fmt.Fprintf(w, "phases: workers=%d chunks=%d read=%v fold=%v read/(read+fold)=%.1f%%\n",
		len(wall), phaseChunks.Load(), read, fold, 100*float64(read)/float64(read+fold))
	fmt.Fprintf(w, "phases: worker wall min=%v p50=%v max=%v sum=%v skew(max/min)=%.3f merge=%v\n",
		wall[0], wall[len(wall)/2], wall[len(wall)-1], sum, float64(wall[len(wall)-1])/float64(wall[0]), merge)
}

// config is one flag per open hypothesis rather than per tunable: 03-technique-recon.md's H1 (work distribution), H5 (table layout) and H7 (mmap end-to-end), plus H3's parse, which 04-asm-kernels.md left split by input.
type config struct {
	Workers int
	BufKiB  int
	SegKiB  int
	Bits    int
	NoCache bool
	Split   string
	Table   string
	IO      string
	Parse   string
	Kernel  string
	Madvise bool
}

// aggregateFile reads path with cfg's strategy and returns the merged per-station aggregate.
//
// Work is divided by BYTE offset, never by row, and a range owns exactly the rows whose FIRST byte falls inside it: a range that does not start at 0 skips the partial row it lands in, and every range finishes the row that straddles its end. Adjacent ranges apply the same rule, so every row is folded exactly once.
func aggregateFile(path string, cfg config) (map[string]*gen.Accumulator, error) {
	if cfg.Workers < 1 {
		return nil, fmt.Errorf("workers must be >= 1, got %d", cfg.Workers)
	}
	if cfg.BufKiB*1024 < 4*maxRow {
		return nil, fmt.Errorf("buffer of %d KiB is too small to hold a row", cfg.BufKiB)
	}
	pk, err := parseMode(cfg.Parse)
	if err != nil {
		return nil, err
	}
	kern, err := kernelMode(cfg.Kernel)
	if err != nil {
		return nil, err
	}
	// A batch kernel always parses branchlessly and checks the format with validTemp, so pairing it with any other -parse would silently measure something other than what the flags say.
	if kern != kernelRow && pk != parseBranchless {
		return nil, fmt.Errorf("-kernel %s has no %s parse arm; use -kernel row with -parse %s", cfg.Kernel, cfg.Parse, cfg.Parse)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return map[string]*gen.Accumulator{}, nil
	}
	if err := requireTrailingNewline(f, size); err != nil {
		return nil, err
	}

	if cfg.NoCache && cfg.IO == "pread" {
		if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), uintptr(fNoCache), 1); e != 0 {
			return nil, fmt.Errorf("fcntl F_NOCACHE: %w", e)
		}
	}

	var mapped []byte
	switch cfg.IO {
	case "pread":
	case "mmap":
		mapped, err = syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
		if err != nil {
			return nil, fmt.Errorf("mmap %d bytes: %w", size, err)
		}
		defer syscall.Munmap(mapped)
		if cfg.Madvise {
			if _, _, e := syscall.Syscall(syscall.SYS_MADVISE, uintptr(unsafe.Pointer(&mapped[0])), uintptr(size), madvWillNeed); e != 0 {
				return nil, fmt.Errorf("madvise MADV_WILLNEED: %w", e)
			}
		}
	default:
		return nil, fmt.Errorf("unknown -io %q, want pread or mmap", cfg.IO)
	}

	tables := make([]*table, cfg.Workers)
	errs := make([]error, cfg.Workers)
	var wg sync.WaitGroup

	// work hands one worker its next byte range, or reports that there is none left. It is the only difference between H1's two arms.
	var work func(w int) (lo, hi int64, ok bool)
	switch cfg.Split {
	case "static":
		span := (size + int64(cfg.Workers) - 1) / int64(cfg.Workers)
		done := make([]bool, cfg.Workers)
		work = func(w int) (int64, int64, bool) {
			if done[w] {
				return 0, 0, false
			}
			done[w] = true
			lo := int64(w) * span
			if lo >= size {
				return 0, 0, false
			}
			return lo, min(lo+span, size), true
		}
	case "cursor":
		if cfg.SegKiB*1024 < 4*maxRow {
			return nil, fmt.Errorf("segment of %d KiB is too small", cfg.SegKiB)
		}
		seg := int64(cfg.SegKiB) * 1024
		var cursor atomic.Int64
		work = func(int) (int64, int64, bool) {
			lo := cursor.Add(seg) - seg
			if lo >= size {
				return 0, 0, false
			}
			return lo, min(lo+seg, size), true
		}
	default:
		return nil, fmt.Errorf("unknown -split %q, want static or cursor", cfg.Split)
	}

	workerWall := make([]time.Duration, cfg.Workers)

	for w := range cfg.Workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			if phasesOn {
				start := time.Now()
				defer func() { workerWall[w] = time.Since(start) }()
			}
			t := newTable(cfg.Bits, cfg.Table == "split")
			tables[w] = t
			var buf []byte
			if mapped == nil {
				buf = make([]byte, cfg.BufKiB*1024)
			}
			for {
				lo, hi, ok := work(w)
				if !ok {
					return
				}
				var err error
				if mapped != nil {
					err = foldMapped(t, mapped, lo, hi, kern, pk)
				} else {
					err = foldRange(f, t, lo, hi, size, buf, kern, pk)
				}
				if err != nil {
					errs[w] = err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	mergeStart := time.Now()
	result := make(map[string]*gen.Accumulator, 1<<14)
	for _, t := range tables {
		if t != nil {
			t.drain(result)
		}
	}
	if phasesOn {
		reportPhases(os.Stderr, workerWall, time.Since(mergeStart))
	}
	return result, nil
}

// parseKind selects how a row's temperature field is turned into tenths AND how its format is rejected; the two are one decision because parseWord folds the second into the first.
type parseKind int

const (
	parseScalar parseKind = iota
	parseBranchless
	parseWord
)

func parseMode(name string) (parseKind, error) {
	switch name {
	case "branchless":
		return parseBranchless, nil
	case "scalar":
		return parseScalar, nil
	case "word":
		return parseWord, nil
	}
	return 0, fmt.Errorf("unknown -parse %q, want branchless, scalar or word", name)
}

// requireTrailingNewline turns the one input shape this reader cannot fold into a named error instead of a confusing row error at the last byte.
func requireTrailingNewline(f *os.File, size int64) error {
	var last [1]byte
	if _, err := f.ReadAt(last[:], size-1); err != nil {
		return err
	}
	if last[0] != '\n' {
		return fmt.Errorf("input does not end with a newline")
	}
	return nil
}

// foldRange reads [lo,hi) in buffer-sized chunks, carrying the partial row at the end of each chunk into the front of the next, and folds the row that straddles hi by reading up to maxRow bytes past it.
//
// A range that does not start at 0 starts reading ONE BYTE EARLY. That byte is what distinguishes "lo is in the middle of a row, skip to the next boundary" from "lo IS a boundary, keep the row that starts there"; without it the second case silently drops one row per aligned boundary, which is one row in every fourteen boundaries on the official key set.
func foldRange(f *os.File, t *table, lo, hi, size int64, buf []byte, k kernel, pk parseKind) error {
	readEnd := min(hi+maxRow, size)
	readStart := lo
	if lo > 0 {
		readStart = lo - 1
	}
	off, carry, first := readStart, 0, true
	for off < readEnd {
		want := min(int64(len(buf)-carry), readEnd-off)
		var t0 time.Time
		if phasesOn {
			t0 = time.Now()
		}
		n, err := f.ReadAt(buf[carry:carry+int(want)], off)
		if phasesOn {
			phaseRead.Add(int64(time.Since(t0)))
			phaseChunks.Add(1)
		}
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			break
		}
		off += int64(n)
		avail := carry + n
		base := off - int64(avail)

		from := 0
		if first && lo > 0 {
			nl := bytes.IndexByte(buf[:avail], '\n')
			if nl < 0 {
				return fmt.Errorf("byte %d: no row boundary in %d bytes", base, avail)
			}
			from = nl + 1
			// A range shorter than one row contains no row START, so it owns nothing: without this it would fold the next range's first row and that row would be counted twice.
			if base+int64(from) >= hi {
				return nil
			}
		}
		first = false

		if base+int64(avail) >= hi {
			// The straddling row may still be incomplete when the buffer is no bigger than the range; in that case fall through, fold the whole rows, and read the rest of it next time round.
			if end, ok := rangeEnd(buf[:avail], base, hi, from); ok {
				return foldTimed(t, buf[from:end], k, pk, base+int64(from))
			}
		}
		nl := bytes.LastIndexByte(buf[:avail], '\n')
		if nl < from {
			return fmt.Errorf("byte %d: row longer than the %d-byte buffer", base, len(buf))
		}
		if err := foldTimed(t, buf[from:nl+1], k, pk, base+int64(from)); err != nil {
			return err
		}
		carry = copy(buf, buf[nl+1:avail])
	}
	return fmt.Errorf("byte %d: the row crossing the end of the range is longer than %d bytes", hi, maxRow)
}

// foldTimed is t.fold with the -phases clock around it, so read and fold are measured at the same two call sites that alternate in the loop.
func foldTimed(t *table, chunk []byte, k kernel, pk parseKind, base int64) error {
	if !phasesOn {
		return t.fold(chunk, k, pk, base)
	}
	t0 := time.Now()
	err := t.fold(chunk, k, pk, base)
	phaseFold.Add(int64(time.Since(t0)))
	return err
}

// foldMapped is foldRange over a mapping: the same ownership rule, no copy, no buffer, and the same one-byte lookback so that a range starting exactly on a row boundary keeps that row.
func foldMapped(t *table, data []byte, lo, hi int64, k kernel, pk parseKind) error {
	from := 0
	if lo > 0 {
		back := lo - 1
		nl := bytes.IndexByte(data[back:min(back+maxRow+1, int64(len(data)))], '\n')
		if nl < 0 {
			return fmt.Errorf("byte %d: no row boundary within %d bytes", lo, maxRow)
		}
		from = int(back) + nl + 1
		if int64(from) >= hi {
			return nil
		}
	}
	end, ok := rangeEnd(data, 0, hi, from)
	if !ok {
		return fmt.Errorf("byte %d: no row boundary at or after the end of the range", hi)
	}
	return t.fold(data[from:end], k, pk, int64(from))
}

// rangeEnd returns the index just past the last row this range owns: the first '\n' at or after hi-1, because a row ending exactly at hi-1 is the last one that STARTS before hi.
// It reports false when data does not reach that newline yet, which is the caller's signal to read more rather than an error.
func rangeEnd(data []byte, base, hi int64, from int) (int, bool) {
	k := int(hi - 1 - base)
	if k < from {
		k = from
	}
	if k >= len(data) {
		return 0, false
	}
	nl := bytes.IndexByte(data[k:], '\n')
	if nl < 0 {
		return 0, false
	}
	return k + nl + 1, true
}
