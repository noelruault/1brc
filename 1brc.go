// Command 1brc aggregates a 1BRC measurements file into min/mean/max per station: go run 1brc.go -in measurements.txt
//
// Generated from code/go by scripts/amalgamate.py, so change that package rather than this file.
// The batch-neon arm is stubbed here because one file cannot link a Plan 9 assembly module.
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// ---- code/go/main.go ----

func main() {
	in := flag.String("in", "measurements.txt", "measurements file to aggregate")
	cfg := config{}
	flag.IntVar(&cfg.Workers, "workers", defaultWorkers(), "parallel readers/aggregators")
	flag.IntVar(&cfg.BufKiB, "buf", defaultBufKiB, "per-worker read buffer, KiB (pread only)")
	flag.IntVar(&cfg.SegKiB, "seg", 2048, "segment claimed per turn, KiB (-split cursor only)")
	flag.IntVar(&cfg.Bits, "bits", 17, "log2 of the per-worker table's bucket count")
	flag.BoolVar(&cfg.NoCache, "nocache", true, "set F_NOCACHE so reads bypass the page cache (pread only)")
	flag.StringVar(&cfg.Split, "split", "static", "work distribution: static | cursor (H1)")
	flag.StringVar(&cfg.Table, "table", "combined", "table layout: combined | split (H5) | quot (H-13) | map (queue item 9; -bits is inert for it)")
	flag.StringVar(&cfg.IO, "io", "pread", "reader: pread | mmap (H7)")
	flag.StringVar(&cfg.Parse, "parse", defaultParse, "temperature parse and format check: branchless | scalar (H3) | word (E-25)")
	flag.StringVar(&cfg.Kernel, "kernel", "row", "tokenizer: row | batch-swar | batch-neon (go-v2-kernels)")
	flag.StringVar(&cfg.Fold, "fold", defaultFold, "row loop: slice | hash | ptr | both (queue items 1 and 5)")
	flag.StringVar(&cfg.Fill, "fill", defaultFill, "read staging: off | sync | ahead (H-14, -io pread only)")
	flag.BoolVar(&cfg.Madvise, "madvise", false, "MADV_WILLNEED the whole mapping first (-io mmap only, H7's rescue)")
	cpuprofile := flag.String("cpuprofile", "", "write a pprof CPU profile here (go-opt-round-2)")
	flag.BoolVar(&phasesOn, "phases", false, "report the read/fold/merge split and the shard skew on stderr (go-opt-round-2)")
	flag.Parse()

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "1brc:", err)
			os.Exit(1)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintln(os.Stderr, "1brc:", err)
			os.Exit(1)
		}
		defer f.Close()
	}

	err := run(*in, cfg, os.Stdout)
	// Stopped before os.Exit rather than deferred: a deferred stop never runs on the error path, which leaves a truncated profile that looks like a real one.
	if *cpuprofile != "" {
		pprof.StopCPUProfile()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "1brc:", err)
		os.Exit(1)
	}
}

// defaultWorkers oversubscribes the cores on purpose: E-17 measured 20 workers on this 15-core machine at 7.5% under one-per-core, slot-corrected and disjoint, because a worker blocked in its read leaves its core to another's fold.
// The ratio is that one measurement generalised so the default still means something on other core counts, not a law; 30 workers also beat 15 and did not separate from 20, so the optimum is a plateau and this is a point inside it.
func defaultWorkers() int { return runtime.NumCPU() * 4 / 3 }

// defaultBufKiB is a measured minimum, not a round number: E-24 swept 512 KiB, 1 MiB, 2 MiB and 4 MiB in one bracketed invocation and 1 MiB won disjoint from all three, reproducing E-23's reversal of E-06 to 0.27%.
// The whole gain is kernel-side — system time falls 25.1% while user CPU stays flat — so a change to the reader's syscall shape (double-buffering, io_uring-style batching) invalidates this number rather than inheriting it.
const defaultBufKiB = 1024

// defaultParse is the fused parse-and-check, kept on E-25's measurement: 1.424 s against 1.498 s and 1.499 s for the two bracket arms, disjoint, with user CPU 6.33% lower for byte-identical output.
// It is the one arm on this board that removed CPU rather than parallel efficiency, and the compute floor it leaves, 1.152 s, is still above the 1.000 s target.
const defaultParse = "word"

// defaultFold is the pointer walk on E-27's measurement: 1.233 s against 1.398 s and 1.399 s for the two bracket arms, disjoint, with user CPU 13.49% lower for byte-identical output.
// It is `ptr` and not `both` because the fuse did not separate from it, and the pre-registered bar was disjoint-or-not-kept; `slice` remains the arm it was measured against.
const defaultFold = "ptr"

// defaultFill is the unstaged reader until H-14's arm is measured against it: `off` is `foldRange`, the loop every published number on this board was taken on.
const defaultFill = "off"

func run(path string, cfg config, out io.Writer) error {
	stations, err := aggregateFile(path, cfg)
	if err != nil {
		return err
	}
	// Buffered: WriteResult emits one small write per station and the 10k case has ten thousand of them.
	w := bufio.NewWriterSize(out, 1<<20)
	if err := WriteResult(w, stations); err != nil {
		return err
	}
	return w.Flush()
}

// ---- code/go/reader.go ----

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
	Fold    string
	Fill    string
	Madvise bool
}

// aggregateFile reads path with cfg's strategy and returns the merged per-station aggregate.
//
// Work is divided by BYTE offset, never by row, and a range owns exactly the rows whose FIRST byte falls inside it: a range that does not start at 0 skips the partial row it lands in, and every range finishes the row that straddles its end. Adjacent ranges apply the same rule, so every row is folded exactly once.
func aggregateFile(path string, cfg config) (map[string]*Accumulator, error) {
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
	fk, err := foldMode(cfg.Fold)
	if err != nil {
		return nil, err
	}
	tk, err := tableMode(cfg.Table)
	if err != nil {
		return nil, err
	}
	flk, err := fillMode(cfg.Fill)
	if err != nil {
		return nil, err
	}
	// A mapping has no read to overlap, so accepting the pair would publish the incumbent's time under the arm's name.
	if flk != fillOff && cfg.IO != "pread" {
		return nil, fmt.Errorf("-fill %s has nothing to overlap under -io %s; it needs -io pread", cfg.Fill, cfg.IO)
	}
	// A batch kernel always parses branchlessly and checks the format with validTemp, so pairing it with any other -parse would silently measure something other than what the flags say.
	if kern != kernelRow && pk != parseBranchless {
		return nil, fmt.Errorf("-kernel %s has no %s parse arm; use -kernel row with -parse %s, or add -parse branchless", cfg.Kernel, cfg.Parse, cfg.Parse)
	}
	// The pointer arms are built against the shape production runs and nothing else, so a combination that would silently fall back to the incumbent loop is refused rather than measured.
	// This fires on the DEFAULT now that the default is `ptr`, which is why the message names the flag to add: every arm that is not -parse word has to ask for -fold slice explicitly.
	if fk != foldSlice && (kern != kernelRow || pk != parseWord) {
		return nil, fmt.Errorf("-fold %s has no %s/%s arm; add -fold slice to run -kernel %s -parse %s on the incumbent loop", cfg.Fold, cfg.Kernel, cfg.Parse, cfg.Kernel, cfg.Parse)
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
		return map[string]*Accumulator{}, nil
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
			t := newTable(cfg.Bits, tk)
			tables[w] = t
			var buf []byte
			var bufs [][]byte
			chunk := cfg.BufKiB * 1024
			if mapped == nil {
				if flk == fillOff {
					buf = make([]byte, chunk)
				} else {
					// Both staged arms get TWO buffers so the pair differs by the goroutine alone; a one-buffer sync arm would also halve the working set and the overlap could not be read off the delta.
					bufs = [][]byte{make([]byte, 2*chunk), make([]byte, 2*chunk)}
				}
			}
			for {
				lo, hi, ok := work(w)
				if !ok {
					return
				}
				var err error
				switch {
				case mapped != nil:
					err = foldMapped(t, mapped, lo, hi, kern, pk, fk)
				case flk == fillOff:
					err = foldRange(f, t, lo, hi, size, buf, kern, pk, fk)
				default:
					err = foldRangeFill(f, t, lo, hi, size, bufs, chunk, flk == fillAhead, kern, pk, fk)
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
	result := make(map[string]*Accumulator, 1<<14)
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

// foldKind selects how the row loop addresses the buffer and where the name's hash gets its word: the two queue items that attack this loop, separately and together.
type foldKind int

const (
	foldSlice foldKind = iota
	foldHash
	foldPtr
	foldBoth
	foldLanes
	foldLanes4
	// foldKindCount must stay last. TestEveryFoldArmIsExercised compares it against the arm registry, so a new arm cannot be added without joining the differential test that proves every arm agrees.
	foldKindCount
)

func foldMode(name string) (foldKind, error) {
	switch name {
	case "slice":
		return foldSlice, nil
	case "hash":
		return foldHash, nil
	case "ptr":
		return foldPtr, nil
	case "both":
		return foldBoth, nil
	case "lanes":
		return foldLanes, nil
	case "lanes4":
		return foldLanes4, nil
	}
	return 0, fmt.Errorf("unknown -fold %q, want slice, hash, ptr, both, lanes or lanes4", name)
}

// fillKind selects how a worker stages its reads against its folds: H-14's double buffer, plus the arm that separates the fill-ahead from the read-shape rewrite it needed to exist.
type fillKind int

const (
	fillOff fillKind = iota
	fillSync
	fillAhead
)

func fillMode(name string) (fillKind, error) {
	switch name {
	case "off":
		return fillOff, nil
	case "sync":
		return fillSync, nil
	case "ahead":
		return fillAhead, nil
	}
	return 0, fmt.Errorf("unknown -fill %q, want off, sync or ahead", name)
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
func foldRange(f *os.File, t *table, lo, hi, size int64, buf []byte, k kernel, pk parseKind, fk foldKind) error {
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
				return foldTimed(t, buf[from:end], k, pk, fk, base+int64(from))
			}
		}
		nl := bytes.LastIndexByte(buf[:avail], '\n')
		if nl < from {
			return fmt.Errorf("byte %d: row longer than the %d-byte buffer", base, len(buf))
		}
		if err := foldTimed(t, buf[from:nl+1], k, pk, fk, base+int64(from)); err != nil {
			return err
		}
		carry = copy(buf, buf[nl+1:avail])
	}
	return fmt.Errorf("byte %d: the row crossing the end of the range is longer than %d bytes", hi, maxRow)
}

// chunkFill is one filled buffer handed from the read to the fold: which buffer, the offset it was read at, how many bytes landed, and the read's error.
type chunkFill struct {
	idx int
	off int64
	n   int
	err error
}

// foldRangeFill is foldRange with the read decoupled from the fold: reads are a FIXED chunk at a fixed offset into a buffer that reserves a whole chunk at the front for the partial row, rather than reads shrunk by the carry into the front of one buffer.
// The prefix is a whole chunk and not maxRow because a maxRow prefix made the two readers disagree ALIGNMENT-DEPENDENTLY on rows between maxRow and the buffer size: whether the carry exceeds maxRow depends on where the chunk boundary fell, so the same file was accepted or rejected according to -workers.
// ahead runs them in their own goroutine so chunk N+1 is in flight while chunk N folds (H-14); with ahead false the identical loop reads inline, and that pair is what isolates the overlap from the read-shape rewrite it needed.
// Ownership is foldRange's rule byte for byte: the one-byte lookback, the skip to the first boundary, the row that straddles hi read up to maxRow past it.
func foldRangeFill(f *os.File, t *table, lo, hi, size int64, bufs [][]byte, chunk int, ahead bool, k kernel, pk parseKind, fk foldKind) error {
	readEnd := min(hi+maxRow, size)
	readStart := lo
	if lo > 0 {
		readStart = lo - 1
	}

	free := make(chan int, len(bufs))
	for i := range bufs {
		free <- i
	}
	filled := make(chan chunkFill, len(bufs))
	done := make(chan struct{})
	defer close(done)

	readChunk := func(off int64) (chunkFill, bool) {
		var i int
		select {
		case i = <-free:
		case <-done:
			return chunkFill{}, false
		}
		want := min(int64(chunk), readEnd-off)
		var t0 time.Time
		if phasesOn {
			t0 = time.Now()
		}
		n, err := f.ReadAt(bufs[i][chunk:int64(chunk)+want], off)
		if phasesOn {
			phaseRead.Add(int64(time.Since(t0)))
			phaseChunks.Add(1)
		}
		if err == io.EOF {
			err = nil
		}
		return chunkFill{idx: i, off: off, n: n, err: err}, true
	}

	var next func() (chunkFill, bool)
	if ahead {
		go func() {
			defer close(filled)
			for off := readStart; off < readEnd; {
				cf, ok := readChunk(off)
				if !ok {
					return
				}
				select {
				case filled <- cf:
				case <-done:
					return
				}
				if cf.err != nil || cf.n == 0 {
					return
				}
				off += int64(cf.n)
			}
		}()
		next = func() (chunkFill, bool) {
			cf, ok := <-filled
			return cf, ok
		}
	} else {
		off := readStart
		next = func() (chunkFill, bool) {
			if off >= readEnd {
				return chunkFill{}, false
			}
			cf, ok := readChunk(off)
			if ok && cf.err == nil {
				off += int64(cf.n)
			}
			return cf, ok
		}
	}

	// pending is the trailing partial row, still living in the PREVIOUS buffer: it is copied into this chunk's reserved prefix before that buffer goes back to the reader, which is the ordering the whole double-buffer rests on.
	var pending []byte
	prev, first := -1, true
	for {
		cf, ok := next()
		if !ok {
			break
		}
		if cf.err != nil {
			return cf.err
		}
		if cf.n == 0 {
			break
		}
		b := bufs[cf.idx]
		carryLen := len(pending)
		start := chunk - carryLen
		copy(b[start:chunk], pending)
		if prev >= 0 {
			free <- prev
		}
		prev = cf.idx
		avail := carryLen + cf.n
		data := b[start : start+avail]
		base := cf.off - int64(carryLen)

		from := 0
		if first && lo > 0 {
			nl := bytes.IndexByte(data, '\n')
			if nl < 0 {
				return fmt.Errorf("byte %d: no row boundary in %d bytes", base, avail)
			}
			from = nl + 1
			if base+int64(from) >= hi {
				return nil
			}
		} else if carryLen > 0 {
			// This window spans carryLen+chunk bytes from a row boundary, so without this the reader would accept a row up to that long, ALIGNMENT-DEPENDENTLY, where foldRange rejects anything past one chunk.
			if nl := bytes.IndexByte(data, '\n'); nl < 0 || nl >= chunk {
				return fmt.Errorf("byte %d: row longer than the %d-byte buffer", base, chunk)
			}
		}
		first = false

		if base+int64(avail) >= hi {
			if end, ok := rangeEnd(data, base, hi, from); ok {
				return foldTimed(t, data[from:end], k, pk, fk, base+int64(from))
			}
		}
		nl := bytes.LastIndexByte(data, '\n')
		if nl < from {
			return fmt.Errorf("byte %d: row longer than the %d-byte buffer", base, chunk)
		}
		if err := foldTimed(t, data[from:nl+1], k, pk, fk, base+int64(from)); err != nil {
			return err
		}
		pending = data[nl+1 : avail]
	}
	return fmt.Errorf("byte %d: the row crossing the end of the range is longer than %d bytes", hi, maxRow)
}

// foldTimed is t.fold with the -phases clock around it, so read and fold are measured at the same two call sites that alternate in the loop.
func foldTimed(t *table, chunk []byte, k kernel, pk parseKind, fk foldKind, base int64) error {
	if !phasesOn {
		return t.fold(chunk, k, pk, fk, base)
	}
	t0 := time.Now()
	err := t.fold(chunk, k, pk, fk, base)
	phaseFold.Add(int64(time.Since(t0)))
	return err
}

// foldMapped is foldRange over a mapping: the same ownership rule, no copy, no buffer, and the same one-byte lookback so that a range starting exactly on a row boundary keeps that row.
func foldMapped(t *table, data []byte, lo, hi int64, k kernel, pk parseKind, fk foldKind) error {
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
	return t.fold(data[from:end], k, pk, fk, int64(from))
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

// ---- code/go/table.go ----

type entry struct {
	key      []byte
	min, max int32
	sum      int64
	count    int32
}

// qentry is H-13's quotiented bucket, 32 bytes against entry's 48: the name's first 8 bytes sit inline instead of a 24-byte slice header, so a probe reads a third fewer bytes and the common compare is one word rather than a call into memequal.
//
// nlen holds len(name)+1 so that a zeroed bucket means EMPTY and a legal empty name still occupies one; word alone cannot say it, because a name of three bytes and the same name padded with NULs mask to the same word.
// ord indexes t.keys and is only read for names longer than 8 bytes, which is where word stops being the whole key: 141 of the 413 official names, and so 34.1% of rows, because Write picks its station uniformly.
// min and max are int16 because that is what buys the 32 bytes; inRange bounds every value before it reaches here and the const below fails to compile if that range ever outgrows the field.
type qentry struct {
	word     uint64
	sum      int64
	count    int32
	ord      int32
	nlen     int32
	min, max int16
}

// These conversions overflow at compile time if the admitted range ever outgrows qentry's int16 min and max.
const (
	_ = int16(MaxTenths)
	_ = int16(MinTenths)
)

// table is per-shard open addressing with linear probing: no lock, no atomic, no sharing, merged only when its shard is done.
//
// hashes is H5 (03-technique-recon.md:63) and q is H-13; keys is dense, one slot per STATION rather than per bucket, so the quotiented layout costs no extra zeroing at startup.
// The mode is one branch per row, taken identically in every layout, so the comparison between them stays fair.
type table struct {
	hashes []uint64
	e      []entry
	q      []qentry
	m      map[string]*entry
	keys   [][]byte
	mask   uint64
	size   int
}

type tableKind int

const (
	tableCombined tableKind = iota
	tableSplit
	tableQuot
	tableMap
)

// tableMode rejects an unknown layout instead of silently running the incumbent, which would report a measurement of the arm it was asked to replace.
func tableMode(s string) (tableKind, error) {
	switch s {
	case "combined":
		return tableCombined, nil
	case "split":
		return tableSplit, nil
	case "quot":
		return tableQuot, nil
	case "map":
		return tableMap, nil
	}
	return 0, fmt.Errorf("unknown -table %q, want combined, split, quot or map", s)
}

func newTable(bits int, kind tableKind) *table {
	t := &table{mask: 1<<bits - 1}
	switch kind {
	case tableQuot:
		t.q = make([]qentry, 1<<bits)
		t.keys = make([][]byte, 0, 1024)
	case tableMap:
		// No size hint on purpose: queue item 9's mechanism is that a map holding only the live stations stays compact where a 1<<bits array does not, and pre-sizing to 1<<bits would build the array this arm exists to avoid.
		t.m = map[string]*entry{}
	default:
		t.e = make([]entry, 1<<bits)
		if kind == tableSplit {
			t.hashes = make([]uint64, 1<<bits)
		}
	}
	return t
}

func (t *table) buckets() int {
	if t.q != nil {
		return len(t.q)
	}
	return len(t.e)
}

// update folds one reading into the table and reports false when the table is FULL, which is the one way a linear probe can fail: with no empty slot the probe loop never ends, and a hang is a worse failure than an error.
// h indexes, and in split mode is also stored with its low bit forced so that zero can mean "empty"; two hashes that differ only in that bit are separated by the full key compare anyway.
// w is h before the mix, which only the quotiented layout reads; the other two take it and drop it, so they keep paying for the arm rather than the arm paying for them.
func (t *table) update(h, w uint64, name []byte, v int32) bool {
	if t.m != nil {
		return t.updateMap(name, v)
	}
	if t.q != nil {
		return t.updateQuot(h, w, name, v)
	}
	if t.size == len(t.e) {
		return false
	}
	i := h & t.mask
	if t.hashes != nil {
		hv := h | 1
		for {
			switch t.hashes[i] {
			case 0:
				t.hashes[i] = hv
				t.insert(int(i), name, v)
				return true
			case hv:
				if bytes.Equal(t.e[i].key, name) {
					t.merge(int(i), v)
					return true
				}
			}
			i = (i + 1) & t.mask
		}
	}
	for {
		if t.e[i].key == nil {
			t.insert(int(i), name, v)
			return true
		}
		if bytes.Equal(t.e[i].key, name) {
			t.merge(int(i), v)
			return true
		}
		i = (i + 1) & t.mask
	}
}

// updateQuot is update over the 32-byte buckets: the probe compares the inline word and the length, and only reaches into keys for a name longer than the word can hold.
//
// The length compare is load-bearing on its own, not a cheap pre-filter for the word: "ab" and "ab\x00" mask to the SAME word, so without it the two names would share a bucket and their readings would merge.
func (t *table) updateQuot(h, w uint64, name []byte, v int32) bool {
	if t.size == len(t.q) {
		return false
	}
	nlen := int32(len(name)) + 1
	long := len(name) > 8
	i := h & t.mask
	for {
		e := &t.q[i]
		switch e.nlen {
		case 0:
			e.word, e.nlen, e.ord = w, nlen, int32(len(t.keys))
			e.min, e.max, e.sum, e.count = int16(v), int16(v), int64(v), 1
			key := make([]byte, len(name))
			copy(key, name)
			t.keys = append(t.keys, key)
			t.size++
			return true
		case nlen:
			if e.word == w && (!long || bytes.Equal(t.keys[e.ord], name)) {
				if int16(v) < e.min {
					e.min = int16(v)
				}
				if int16(v) > e.max {
					e.max = int16(v)
				}
				e.sum += int64(v)
				e.count++
				return true
			}
		}
		i = (i + 1) & t.mask
	}
}

// updateMap is queue item 9: Go's runtime map instead of the open-addressed array, kept behind a flag by 05-go-techniques.md:108 because it measured 3.7% FASTER than the best array on the 10,000-station key set and 15.8% slower on the official 413.
//
// It ignores h and w, which the caller computed anyway: removing that would need a per-row branch in the fold loop, which would charge every other layout for this arm's existence, so the dead hash is priced into this arm's number rather than out of the incumbent's.
// The lookup is the map-access-with-string([]byte) form the compiler turns into a no-copy probe; the INSERT converts for real, which is what keeps the key from aliasing the read buffer the worker is about to refill.
// It can never report full, so unlike a linear probe it has no failure mode to guard: a map with no empty slot grows.
func (t *table) updateMap(name []byte, v int32) bool {
	if e := t.m[string(name)]; e != nil {
		if v < e.min {
			e.min = v
		}
		if v > e.max {
			e.max = v
		}
		e.sum += int64(v)
		e.count++
		return true
	}
	t.m[string(name)] = &entry{min: v, max: v, sum: int64(v), count: 1}
	t.size++
	return true
}

func (t *table) insert(i int, name []byte, v int32) {
	key := make([]byte, len(name))
	copy(key, name)
	t.e[i] = entry{key: key, min: v, max: v, sum: int64(v), count: 1}
	t.size++
}

func (t *table) merge(i int, v int32) {
	e := &t.e[i]
	if v < e.min {
		e.min = v
	}
	if v > e.max {
		e.max = v
	}
	e.sum += int64(v)
	e.count++
}

// fold aggregates every row in data, which must begin at a row boundary and end immediately after a '\n'.
//
// Every kernel here over-reads 8 bytes, so each stops early and foldTail's scalar path closes the rest without ever over-reading: that is what removes any need for padded buffers or a guard page.
// The kernel is chosen ONCE per buffer, because a per-row switch would charge every arm for the comparison the batch arms exist to remove.
func (t *table) fold(data []byte, k kernel, pk parseKind, fk foldKind, base int64) error {
	var (
		pos int
		err error
	)
	switch k {
	case kernelBatchSWAR:
		pos, err = t.foldBatchSWAR(data, base)
	case kernelBatchNEON:
		pos, err = t.foldBatchNEON(data, base)
	default:
		switch fk {
		case foldHash:
			pos, err = t.foldRowsHash(data, base)
		case foldPtr:
			pos, err = t.foldRowsPtr(data, base, false)
		case foldBoth:
			pos, err = t.foldRowsPtr(data, base, true)
		case foldLanes:
			pos, err = t.foldRowsLanes(data, base)
		case foldLanes4:
			pos, err = t.foldRowsLanes4(data, base)
		default:
			pos, err = t.foldRows(data, pk, base)
		}
	}
	if err != nil {
		return err
	}
	return t.foldTail(data, pos, base)
}

// foldRows is v1's per-row kernel: rescan from each row's first byte, one separator search and one parse per row.
func (t *table) foldRows(data []byte, pk parseKind, base int64) (int, error) {
	pos := 0
	for pos+maxRow <= len(data) {
		sep, semi := indexDelim(data[pos:])
		if sep < 0 || !semi {
			return 0, rowError(base+int64(pos), data[pos:])
		}
		if pos+sep+9 > len(data) {
			break
		}
		field := data[pos+sep+1:]
		var (
			v    int32
			next int
		)
		// The incumbent is the FIRST test on purpose: it keeps paying the one loop-invariant compare it paid before this arm existed, and the new arm pays the extra one, so the bias runs against the hypothesis rather than for it.
		if pk == parseBranchless {
			v, next = parseTempBranchless(field)
			if next == 0 || !validTemp(field, next) || !inRange(v) {
				return 0, rowError(base+int64(pos), data[pos:])
			}
		} else if pk == parseWord {
			var ok bool
			v, next, ok = parseTempWord(field)
			if !ok || !inRange(v) {
				return 0, rowError(base+int64(pos), data[pos:])
			}
		} else {
			var ok bool
			v, next, ok = parseTempScalar(field)
			if !ok || !inRange(v) {
				return 0, rowError(base+int64(pos), data[pos:])
			}
		}
		name := data[pos : pos+sep]
		kw := maskWord(binary.LittleEndian.Uint64(data[pos:]), sep)
		if !t.update(mixWord(kw), kw, name, v) {
			return 0, t.fullError(base + int64(pos))
		}
		pos += sep + 1 + next
	}
	return pos, nil
}

// foldRowsHash is foldRows with the name's hash taken from the word the separator scan already loaded (queue item 1), and nothing else changed: same slice walk, same bounds, same parse.
// It is the word-parse arm only, which is what the -fold guard in aggregateFile enforces, because the arm exists to be measured against the shape production runs.
func (t *table) foldRowsHash(data []byte, base int64) (int, error) {
	pos := 0
	for pos+maxRow <= len(data) {
		sep, semi, w0 := indexDelimAt(unsafe.Pointer(unsafe.SliceData(data[pos:])), len(data)-pos)
		if sep < 0 || !semi {
			return 0, rowError(base+int64(pos), data[pos:])
		}
		if pos+sep+9 > len(data) {
			break
		}
		v, next, ok := parseTempWord(data[pos+sep+1:])
		if !ok || !inRange(v) {
			return 0, rowError(base+int64(pos), data[pos:])
		}
		name := data[pos : pos+sep]
		kw := maskWord(w0, sep)
		if !t.update(mixWord(kw), kw, name, v) {
			return 0, t.fullError(base + int64(pos))
		}
		pos += sep + 1 + next
	}
	return pos, nil
}

// foldRowsPtr walks the buffer with unsafe.Add instead of re-slicing it at every row (queue item 5), and takes the hash's word from the scan when fuse is set, which is queue item 1 on top of it.
//
// Every load is one the slice walk makes too and the loop keeps foldRows's own bound, so the fast path still stops maxRow short of the end and foldTail closes the rest.
// What keeps the names handed to update pointing at live memory is the CALLER: foldRange owns buf and foldMapped's mapping is unmapped by a defer in aggregateFile, both outliving this call. Taking data by value here does not by itself keep it reachable, so a caller that stops owning its buffer has to add the KeepAlive.
func (t *table) foldRowsPtr(data []byte, base int64, fuse bool) (int, error) {
	pos, n := 0, len(data)
	p := unsafe.Pointer(unsafe.SliceData(data))
	for pos+maxRow <= n {
		row := unsafe.Add(p, pos)
		sep, semi, w0 := indexDelimAt(row, n-pos)
		if sep < 0 || !semi {
			return 0, rowError(base+int64(pos), data[pos:])
		}
		// This guard is the ONLY thing bounding the load below: the incumbent's slice read would panic if it were loosened and this one reads past the buffer in silence.
		if pos+sep+9 > n {
			break
		}
		v, next, ok := parseTempWordFrom(*(*uint64)(unsafe.Add(row, sep+1)))
		if !ok || !inRange(v) {
			return 0, rowError(base+int64(pos), data[pos:])
		}
		if !fuse {
			w0 = *(*uint64)(row)
		}
		kw := maskWord(w0, sep)
		if !t.update(mixWord(kw), kw, unsafe.Slice((*byte)(row), sep), v) {
			return 0, t.fullError(base + int64(pos))
		}
		pos += sep + 1 + next
	}
	return pos, nil
}

// foldRowsLanes runs TWO row cursors over one buffer so their dependency chains overlap: a row's start is not known until its own parse retires, so one cursor gives the core no independent work to fill the stall with.
//
// The split is at a ROW BOUNDARY, which is what makes the aggregate identical: each lane owns whole rows and min/max/sum/count commute.
// The loop bound is the lane's end but over-reads are bounded by n, so a lane may read past its end into another lane's bytes and still be inside the buffer.
func (t *table) foldRowsLanes(data []byte, base int64) (int, error) {
	n := len(data)
	split := laneSplit(data, n/2)
	// One lane cannot overlap anything, and a buffer too small to split is the tail's job anyway.
	if split <= 0 || split >= n-maxRow {
		return t.foldRowsPtr(data, base, false)
	}
	p := unsafe.Pointer(unsafe.SliceData(data))
	aPos, bPos := 0, split

	for aPos+maxRow <= split && bPos+maxRow <= n {
		rowA, rowB := unsafe.Add(p, aPos), unsafe.Add(p, bPos)

		// Both scans issue before either result is consumed; this is the whole point of the kernel and the reason the two are not folded into a helper.
		sepA, semiA, _ := indexDelimAt(rowA, split-aPos)
		sepB, semiB, _ := indexDelimAt(rowB, n-bPos)
		if sepA < 0 || !semiA {
			return 0, t.canonicalRowError(data, base, rowError(base+int64(aPos), data[aPos:]))
		}
		if sepB < 0 || !semiB {
			return 0, t.canonicalRowError(data, base, rowError(base+int64(bPos), data[bPos:]))
		}
		if bPos+sepB+9 > n {
			break
		}

		vA, nextA, okA := parseTempWordFrom(*(*uint64)(unsafe.Add(rowA, sepA+1)))
		vB, nextB, okB := parseTempWordFrom(*(*uint64)(unsafe.Add(rowB, sepB+1)))
		if !okA || !inRange(vA) {
			return 0, t.canonicalRowError(data, base, rowError(base+int64(aPos), data[aPos:]))
		}
		if !okB || !inRange(vB) {
			return 0, t.canonicalRowError(data, base, rowError(base+int64(bPos), data[bPos:]))
		}

		kwA := maskWord(*(*uint64)(rowA), sepA)
		kwB := maskWord(*(*uint64)(rowB), sepB)
		if !t.update(mixWord(kwA), kwA, unsafe.Slice((*byte)(rowA), sepA), vA) {
			return 0, t.fullError(base + int64(aPos))
		}
		if !t.update(mixWord(kwB), kwB, unsafe.Slice((*byte)(rowB), sepB), vB) {
			return 0, t.fullError(base + int64(bPos))
		}
		aPos += sepA + 1 + nextA
		bPos += sepB + 1 + nextB
	}

	// Whichever lane still has bulk rows finishes alone, then lane A's remainder is closed against its own end so it never reads into lane B's rows.
	if bPos < n {
		got, err := t.foldRowsPtr(data[bPos:], base+int64(bPos), false)
		if err != nil {
			return 0, t.canonicalRowError(data, base, err)
		}
		bPos += got
	}
	if err := t.foldTail(data[:split], aPos, base+int64(aPos)); err != nil {
		return 0, t.canonicalRowError(data, base, err)
	}
	return bPos, nil
}

// foldRowsLanes4 is foldRowsLanes with four cursors instead of two.
//
// Two cursors measured -5.2% of user CPU by giving the core a second dependency chain; four tests whether the out-of-order window has room for more, or whether register and cache pressure over four live rows takes it back.
// The four are held in fixed-size arrays indexed by CONSTANTS so each stays in its own register: a loop over lanes would serialise them through memory and remove the only thing this kernel exists to add.
func (t *table) foldRowsLanes4(data []byte, base int64) (int, error) {
	n := len(data)
	var pos, end [4]int
	for i := range 4 {
		start := 0
		if i > 0 {
			start = laneSplit(data, n*i/4)
			if start <= 0 {
				return t.foldRowsLanes(data, base)
			}
		}
		pos[i] = start
		if i > 0 {
			end[i-1] = start
		}
	}
	end[3] = n
	// Lanes must be strictly increasing and each wide enough to hold a row, or the split degenerates and two cursors are the better shape.
	for i := range 4 {
		if pos[i]+maxRow > end[i] {
			return t.foldRowsLanes(data, base)
		}
	}

	p := unsafe.Pointer(unsafe.SliceData(data))
	var row [4]unsafe.Pointer
	var sep [4]int
	var semi [4]bool
	var v [4]int32
	var next [4]int

	for pos[0]+maxRow <= end[0] && pos[1]+maxRow <= end[1] && pos[2]+maxRow <= end[2] && pos[3]+maxRow <= end[3] {
		row[0], row[1] = unsafe.Add(p, pos[0]), unsafe.Add(p, pos[1])
		row[2], row[3] = unsafe.Add(p, pos[2]), unsafe.Add(p, pos[3])

		sep[0], semi[0], _ = indexDelimAt(row[0], end[0]-pos[0])
		sep[1], semi[1], _ = indexDelimAt(row[1], end[1]-pos[1])
		sep[2], semi[2], _ = indexDelimAt(row[2], end[2]-pos[2])
		sep[3], semi[3], _ = indexDelimAt(row[3], end[3]-pos[3])

		for i := range 4 {
			if sep[i] < 0 || !semi[i] {
				return 0, t.canonicalRowError(data, base, rowError(base+int64(pos[i]), data[pos[i]:]))
			}
		}
		if pos[3]+sep[3]+9 > n {
			break
		}

		var ok [4]bool
		v[0], next[0], ok[0] = parseTempWordFrom(*(*uint64)(unsafe.Add(row[0], sep[0]+1)))
		v[1], next[1], ok[1] = parseTempWordFrom(*(*uint64)(unsafe.Add(row[1], sep[1]+1)))
		v[2], next[2], ok[2] = parseTempWordFrom(*(*uint64)(unsafe.Add(row[2], sep[2]+1)))
		v[3], next[3], ok[3] = parseTempWordFrom(*(*uint64)(unsafe.Add(row[3], sep[3]+1)))

		for i := range 4 {
			if !ok[i] || !inRange(v[i]) {
				return 0, t.canonicalRowError(data, base, rowError(base+int64(pos[i]), data[pos[i]:]))
			}
		}

		for i := range 4 {
			kw := maskWord(*(*uint64)(row[i]), sep[i])
			if !t.update(mixWord(kw), kw, unsafe.Slice((*byte)(row[i]), sep[i]), v[i]) {
				return 0, t.fullError(base + int64(pos[i]))
			}
			pos[i] += sep[i] + 1 + next[i]
		}
	}

	// Each lane closes its own remainder against its own end, so no lane reads rows another lane owns.
	for i := range 4 {
		if err := t.foldTail(data[:end[i]], pos[i], base+int64(pos[i])); err != nil {
			return 0, t.canonicalRowError(data, base, err)
		}
	}
	return n, nil
}

// canonicalRowError re-walks the buffer serially so a multi-cursor kernel reports the error the single-cursor walk would.
// Cursors retire rows out of order, so the first bad row a lane meets is not the first bad row in the FILE, and the offset in the message would depend on the lane count.
// The table is discarded whenever a fold returns an error, so re-walking costs nothing that matters and only ever runs on malformed input.
func (t *table) canonicalRowError(data []byte, base int64, found error) error {
	if _, err := t.foldRowsPtr(data, base, false); err != nil {
		return err
	}
	if err := t.foldTail(data, 0, base); err != nil {
		return err
	}
	return found
}

// laneSplit returns the index just past the first newline at or after mid, or -1 when the buffer has none there.
func laneSplit(data []byte, mid int) int {
	for i := mid; i < len(data); i++ {
		if data[i] == '\n' {
			return i + 1
		}
	}
	return -1
}

// foldTail closes the rows a kernel stopped short of, with the scalar path that never over-reads.
func (t *table) foldTail(data []byte, pos int, base int64) error {
	for pos < len(data) {
		sep, semi := indexDelim(data[pos:])
		if sep < 0 || !semi {
			return rowError(base+int64(pos), data[pos:])
		}
		v, next, ok := parseTempScalar(data[pos+sep+1:])
		if !ok || !inRange(v) {
			return rowError(base+int64(pos), data[pos:])
		}
		name := data[pos : pos+sep]
		kw := maskName(name)
		if !t.update(mixWord(kw), kw, name, v) {
			return t.fullError(base + int64(pos))
		}
		pos += sep + 1 + next
	}
	return nil
}

// drain folds this shard's entries into the shared result map. It runs once per shard, so it is allowed to be obvious.
//
// The three loops are not factored into one helper because a shared one would take the key as a string, and the two array layouts hold theirs as []byte: the conversion at the call site allocates per station per shard, which would put an allocation into the incumbent's merge that queue item 10 is meant to measure without.
func (t *table) drain(into map[string]*Accumulator) {
	for name, e := range t.m {
		a := into[name]
		if a == nil {
			into[name] = &Accumulator{Min: Tenths(e.min), Max: Tenths(e.max), Sum: Tenths(e.sum), Count: int64(e.count)}
			continue
		}
		if Tenths(e.min) < a.Min {
			a.Min = Tenths(e.min)
		}
		if Tenths(e.max) > a.Max {
			a.Max = Tenths(e.max)
		}
		a.Sum += Tenths(e.sum)
		a.Count += int64(e.count)
	}
	for i := range t.q {
		e := &t.q[i]
		if e.nlen == 0 {
			continue
		}
		key := t.keys[e.ord]
		a := into[string(key)]
		if a == nil {
			into[string(key)] = &Accumulator{Min: Tenths(e.min), Max: Tenths(e.max), Sum: Tenths(e.sum), Count: int64(e.count)}
			continue
		}
		if Tenths(e.min) < a.Min {
			a.Min = Tenths(e.min)
		}
		if Tenths(e.max) > a.Max {
			a.Max = Tenths(e.max)
		}
		a.Sum += Tenths(e.sum)
		a.Count += int64(e.count)
	}
	for i := range t.e {
		e := &t.e[i]
		if e.key == nil {
			continue
		}
		a := into[string(e.key)]
		if a == nil {
			into[string(e.key)] = &Accumulator{Min: Tenths(e.min), Max: Tenths(e.max), Sum: Tenths(e.sum), Count: int64(e.count)}
			continue
		}
		if Tenths(e.min) < a.Min {
			a.Min = Tenths(e.min)
		}
		if Tenths(e.max) > a.Max {
			a.Max = Tenths(e.max)
		}
		a.Sum += Tenths(e.sum)
		a.Count += int64(e.count)
	}
}

func (t *table) fullError(offset int64) error {
	return fmt.Errorf("byte %d: all %d table buckets are occupied; raise -bits", offset, t.buckets())
}

func rowError(offset int64, rest []byte) error {
	row := rest
	if i := bytes.IndexByte(row, '\n'); i >= 0 {
		row = row[:i]
	}
	if len(row) > maxRow {
		row = row[:maxRow]
	}
	return fmt.Errorf("byte %d: %q is not a valid row", offset, row)
}

// ---- code/go/kernel.go ----

// The kernels here are reimplemented rather than imported from 1brc/code/asm, which is the kernel-research module and links Plan 9 assembly this ticket deliberately does not ship (`go-v1-parallel`: "no asm yet").
// Provenance of both constants blocks, and which upstream author they come from, is in LICENSES.md.

// maxRow is the longest row the fast path is willing to assume fits: 100 name bytes (the challenge's limit), ';', "-99.9", '\n', and headroom.
// The fast path only runs while a whole row plus its over-fetch is inside the buffer, which is what lets every load below be unconditional.
const maxRow = 128

const (
	semicolonBroadcast = 0x3B3B3B3B3B3B3B3B
	newlineBroadcast   = 0x0A0A0A0A0A0A0A0A
	swarLow            = 0x0101010101010101
	swarHigh           = 0x8080808080808080
)

// indexDelim finds the first ';' or '\n' eight bytes at a time and reports which one it found.
//
// A borrow chain can set a lane's high bit ABOVE a match but never below one, so the lowest set bit is always the first match.
// Both needles are scanned, not just ';', because a row with no separator would otherwise have its name run into the NEXT row and be folded as a station that does not exist: the reference rejects that row, so this has to as well. It costs four more integer ops per word and is the price of agreeing with the reference on malformed input.
func indexDelim(b []byte) (idx int, semi bool) {
	i := 0
	for ; i+8 <= len(b); i += 8 {
		w := binary.LittleEndian.Uint64(b[i:])
		xs := w ^ semicolonBroadcast
		xn := w ^ newlineBroadcast
		m := ((xs-swarLow)&^xs | (xn-swarLow)&^xn) & swarHigh
		if m != 0 {
			k := i + bits.TrailingZeros64(m)>>3
			return k, b[k] == ';'
		}
	}
	for ; i < len(b); i++ {
		if b[i] == ';' || b[i] == '\n' {
			return i, b[i] == ';'
		}
	}
	return -1, false
}

// indexDelimAt is indexDelim over a raw pointer, handing back the FIRST word it loaded so the caller can hash the name from it instead of loading the same bytes again (queue item 1).
//
// The loop is rotated rather than given an i == 0 test, because a per-word branch to save a per-row load would cost more than it saves.
// w0 is zero when n < 8 and no word was loaded, which is the tail's shape; callers there hash with hashName, not hashWord.
func indexDelimAt(p unsafe.Pointer, n int) (idx int, semi bool, w0 uint64) {
	i := 0
	if n >= 8 {
		w0 = *(*uint64)(p)
		w := w0
		for {
			xs := w ^ semicolonBroadcast
			xn := w ^ newlineBroadcast
			m := ((xs-swarLow)&^xs | (xn-swarLow)&^xn) & swarHigh
			if m != 0 {
				k := i + bits.TrailingZeros64(m)>>3
				return k, *(*byte)(unsafe.Add(p, k)) == ';', w0
			}
			i += 8
			if i+8 > n {
				break
			}
			w = *(*uint64)(unsafe.Add(p, i))
		}
	}
	for ; i < n; i++ {
		if c := *(*byte)(unsafe.Add(p, i)); c == ';' || c == '\n' {
			return i, c == ';', w0
		}
	}
	return -1, false, w0
}

const (
	parseDotBits  = 0x10101000
	parseDigitsHi = 0x0F000F0F00
	parseMagic    = 0x640a0001
)

// parseTempBranchless parses a one-decimal temperature at the start of b with no data-dependent branch, returning tenths and the offset of the byte after the row's '\n'.
// It loads 8 bytes unconditionally, so len(b) >= 8. It reports next == 0 for a field that cannot be a temperature at all, and otherwise the caller still has to check that the byte it landed on really is a '\n'.
func parseTempBranchless(b []byte) (tenths int32, next int) {
	w := binary.LittleEndian.Uint64(b)
	dot := bits.TrailingZeros64(^w & parseDotBits)
	// The mask only has bits at 12, 20 and 28, so dot is one of those or 64 when no byte in 1-3 can be the '.'; 64 would shift by a negative amount and panic, and it means the field is not a temperature.
	if dot > 28 {
		return 0, 0
	}
	signed := int64(^w<<59) >> 63
	digits := int64((w & ^uint64(signed&0xFF)) << (28 - dot) & parseDigitsHi)
	abs := (digits * parseMagic >> 32) & 0x3FF
	return int32((abs ^ signed) - signed), dot>>3 + 3
}

const (
	digitHighNibbles = 0xF0F0F0F0F0F0F0F0
	digitLowNibbles  = 0x0F0F0F0F0F0F0F0F
	digitThrees      = 0x3030303030303030
	digitSixes       = 0x0606060606060606
	dotThenNewline   = uint64('.') | uint64('\n')<<16
	dotNewlineMask   = uint64(0xFF) | uint64(0xFF)<<16
)

// nonDigitMask sets bits in the high nibble of every lane of w that is not an ASCII digit, and leaves every digit lane zero.
//
// The low-nibble add cannot carry into the next lane: masking to 0x0F first bounds each lane at 0x15, so the eight tests stay independent.
// A lane is a digit exactly when its high nibble is 3 and its low nibble is under 10, which is why both halves are needed: ':' (0x3A) passes the high-nibble test and 'b' (0x62) passes the low-nibble one.
func nonDigitMask(w uint64) uint64 {
	return (w^digitThrees)&digitHighNibbles | ((w&digitLowNibbles)+digitSixes)&digitHighNibbles
}

// parseTempWord is parseTempBranchless with validTemp's format rejection folded into the same 8-byte word, so the shape is established from bits already in a register instead of from four to six dependent byte compares behind an unpredictable three-way switch on next.
//
// It must accept and reject byte for byte what parseTempBranchless plus validTemp accept and reject, and TestParseTempWordMatchesTheByteCheck pins that over every 6-byte string in the alphabet the shape is built from.
// The four rejections are ORed into one accumulator so no test can branch: the '.' and '\n' at their fixed offsets from the dot lane, the digit lanes, the sign byte when the parse read one, and the digit COUNT.
// Each is load-bearing alone, measured by dropping one at a time: only the count rejects "100.0", only the sign byte rejects "\x005.0", only the digit lanes reject "1x.5", only the '\n' lane rejects "1.5x" and only the '.' lane rejects "1-5".
// The one branch kept is parseTempBranchless's own dot > 28 guard, which a negative shift would otherwise panic on.
func parseTempWord(b []byte) (tenths int32, next int, ok bool) {
	return parseTempWordFrom(binary.LittleEndian.Uint64(b))
}

// parseTempWordFrom is parseTempWord for a caller that already holds the field's 8-byte word, which is the pointer walk (queue item 5).
// The split is a refactor and nothing else: parseTempWord is the same function with the load in front of it, so the exhaustive differential test covers both.
func parseTempWordFrom(w uint64) (tenths int32, next int, ok bool) {
	dot := bits.TrailingZeros64(^w & parseDotBits)
	if dot > 28 {
		return 0, 0, false
	}
	signed := int64(^w<<59) >> 63
	digits := int64((w & ^uint64(signed&0xFF)) << (28 - dot) & parseDigitsHi)
	abs := (digits * parseMagic >> 32) & 0x3FF

	shift := uint(dot - 4)
	neg := uint64(signed) & 1
	bad := (w ^ dotThenNewline<<shift) & (dotNewlineMask << shift)
	// The lane just under the dot and the lane at neg are the whole digit run for a run of one or two: at length one they are the same lane, at length two they are its ends.
	bad |= nonDigitMask(w) & (uint64(0xF0)<<(shift+8) | uint64(0xF0)<<(shift-8) | uint64(0xF0)<<(8*neg))
	bad |= (w ^ '-') & 0xFF & uint64(signed)
	bad |= uint64(dot>>3-int(neg)-1) &^ 1
	return int32((abs ^ signed) - signed), dot>>3 + 3, bad == 0
}

// parseTempScalar is the branchy alternative H3 left alive, and also the tail path: it never over-reads, so it is what closes a buffer's last rows.
func parseTempScalar(b []byte) (tenths int32, next int, ok bool) {
	i, neg := 0, false
	if i < len(b) && b[i] == '-' {
		neg = true
		i++
	}
	whole, digits := int32(0), 0
	for ; i < len(b) && b[i] >= '0' && b[i] <= '9'; i++ {
		whole = whole*10 + int32(b[i]-'0')
		digits++
	}
	if digits == 0 || digits > 2 || i+2 >= len(b) || b[i] != '.' {
		return 0, 0, false
	}
	if b[i+1] < '0' || b[i+1] > '9' || b[i+2] != '\n' {
		return 0, 0, false
	}
	tenths = whole*10 + int32(b[i+1]-'0')
	if neg {
		tenths = -tenths
	}
	return tenths, i + 3, true
}

const hashMultiplier = 0x9E3779B97F4A7C15

// maskWord keeps the name's own bytes out of the word the row was read in, clearing everything at or above nameLen.
// The quotiented table (H-13) stores this word as the bucket's inline key, so it is the identity of a station and not only an input to the hash: two paths that mask differently would give one station two buckets.
func maskWord(w uint64, nameLen int) uint64 {
	if nameLen < 8 {
		w &= ^uint64(0) >> ((8 - nameLen) * 8)
	}
	return w
}

// mixWord turns a masked key word into a bucket index.
func mixWord(w uint64) uint64 { return (w ^ w>>29) * hashMultiplier }

// hashWord hashes a name from the first 8 bytes of the word it starts in, masking off everything at or above nameLen.
// 05-go-techniques.md measured that 8 bytes leave exactly one collision across the 413 official stations and none across the 10k set, and the table resolves it with a full compare.
func hashWord(w uint64, nameLen int) uint64 { return mixWord(maskWord(w, nameLen)) }

// maskName is maskWord for a caller that has the name but not the word, and it MUST agree with maskWord byte for byte.
// It never reads past the name, which is what makes it the tail path's masker.
func maskName(name []byte) uint64 {
	if len(name) >= 8 {
		return binary.LittleEndian.Uint64(name)
	}
	var w uint64
	for i := len(name) - 1; i >= 0; i-- {
		w = w<<8 | uint64(name[i])
	}
	return w
}

// hashName is hashWord for a caller that has the name but not the word, and it MUST agree with hashWord byte for byte: a station hashed two ways would occupy two buckets and split its own aggregate.
// TestHashPathsAgree pins that.
func hashName(name []byte) uint64 { return mixWord(maskName(name)) }

func isDigit(c byte) bool { return c-'0' <= 9 }

// validTemp reports whether the field really is the `-?\d{1,2}\.\d\n` that parseTempBranchless assumes and cannot itself check; next is the parse's own answer for where the row ends, which fixes the shape.
//
// It exists because the parse is happy to read a name containing the separator ("b;1.0", from a row whose real name is "a;b") and a three-digit temperature ("100.0") as plausible values, and the reference rejects both. Without this the two implementations would disagree only on rows no byte-comparison of clean data ever sees.
func validTemp(b []byte, next int) bool {
	switch next {
	case 4:
		return isDigit(b[0]) && b[1] == '.' && isDigit(b[2]) && b[3] == '\n'
	case 5:
		return (isDigit(b[0]) || b[0] == '-') && isDigit(b[1]) && b[2] == '.' && isDigit(b[3]) && b[4] == '\n'
	case 6:
		return b[0] == '-' && isDigit(b[1]) && isDigit(b[2]) && b[3] == '.' && isDigit(b[4]) && b[5] == '\n'
	}
	return false
}

// inRange reports whether tenths is a temperature the generator could have written; the reference rejects anything else, so this binary has to as well or the two disagree only on files nobody byte-compares.
func inRange(tenths int32) bool {
	return tenths >= int32(MinTenths) && tenths <= int32(MaxTenths)
}

// ---- code/go/batch.go ----

// kernel selects how a buffer is split into rows. The three arms are what `go-v2-kernels` measures against each other end to end.
type kernel int

const (
	kernelRow kernel = iota
	kernelBatchSWAR
	kernelBatchNEON
)

// batchWindowNEON is the width of one asm.DelimMask32 compare, and batchWindowSWAR is one 64-bit word.
// Both loops stop batchTailSlack bytes early so that the branchless parse's unconditional 8-byte load, which may start on the last separator a window reports, still lands inside the buffer.
const (
	batchWindowNEON = 32
	batchWindowSWAR = 8
	batchTailSlack  = 8
)

const swarLow7Bits = 0x7F7F7F7F7F7F7F7F

// zeroByteMask returns a mask with bit 8k+7 set for every zero byte of w, and no other bits.
//
// indexDelim's cheaper `(w-low) &^ w & high` form is not usable here. Its borrow chain can set a lane's high bit above a real match, which is harmless when only the LOWEST set bit is read and is a wrong row boundary when every bit is drained, which is what a batch kernel does.
func zeroByteMask(w uint64) uint64 {
	return ^(((w & swarLow7Bits) + swarLow7Bits) | w) & swarHigh
}

// foldBatchSWAR is the batch shape without the vector unit: one pass over the buffer, both needles drained from each 8-byte word in address order, no per-row rescan and no assembly call.
// It returns the offset of the first row it did not fold, which its caller finishes with the scalar tail.
func (t *table) foldBatchSWAR(data []byte, base int64) (int, error) {
	pendingSep, rowStart := -1, 0
	for pos := 0; pos+batchWindowSWAR+batchTailSlack <= len(data); pos += batchWindowSWAR {
		w := binary.LittleEndian.Uint64(data[pos:])
		semi, nl := zeroByteMask(w^semicolonBroadcast), zeroByteMask(w^newlineBroadcast)
		for semi|nl != 0 {
			s, l := 64, 64
			if semi != 0 {
				s = bits.TrailingZeros64(semi)
			}
			if nl != 0 {
				l = bits.TrailingZeros64(nl)
			}
			if s < l {
				if pendingSep < 0 {
					pendingSep = pos + s>>3
				}
				semi &= semi - 1
				continue
			}
			next, err := t.foldBatchRow(data, base, rowStart, pendingSep, pos+l>>3)
			if err != nil {
				return 0, err
			}
			rowStart, pendingSep = next, -1
			nl &= nl - 1
		}
	}
	return rowStart, nil
}

// foldBatchNEON is the one arm that cannot cross into a single file, since its 32-byte compare is a call into the Plan 9 assembly module.
func (t *table) foldBatchNEON(data []byte, base int64) (int, error) {
	return 0, fmt.Errorf("-kernel batch-neon needs the assembly module, so run it from code/go rather than this file")
}

// foldBatchRow folds the row [rowStart,end] that the drain has just closed, and returns where the next row starts.
//
// pendingSep is the FIRST ';' seen since rowStart, which is Aggregate's rule; a row whose newline arrives with no separator pending is the malformed shape the reference rejects, and so does this.
// The rejections mirror the per-row path exactly, because a kernel that accepts a row the other kernels reject changes the answer rather than the speed.
func (t *table) foldBatchRow(data []byte, base int64, rowStart, pendingSep, end int) (int, error) {
	if pendingSep < 0 {
		return 0, rowError(base+int64(rowStart), data[rowStart:])
	}
	field := data[pendingSep+1:]
	v, next := parseTempBranchless(field)
	if next == 0 || !validTemp(field, next) || !inRange(v) {
		return 0, rowError(base+int64(rowStart), data[rowStart:])
	}
	// The parse is NOT checked against end: validTemp already requires a '\n' at pendingSep+next with only digits before it, and end is the lowest undrained newline above pendingSep, so they are the same byte.
	name := data[rowStart:pendingSep]
	kw := maskWord(binary.LittleEndian.Uint64(data[rowStart:]), len(name))
	if !t.update(mixWord(kw), kw, name, v) {
		return 0, t.fullError(base + int64(rowStart))
	}
	return end + 1, nil
}

func kernelMode(name string) (kernel, error) {
	switch name {
	case "row":
		return kernelRow, nil
	case "batch-swar":
		return kernelBatchSWAR, nil
	case "batch-neon":
		return kernelBatchNEON, nil
	}
	return 0, fmt.Errorf("unknown -kernel %q, want row, batch-swar or batch-neon", name)
}

// ---- code/gen: the temperature arithmetic and the output format both sides must agree on ----

// Tenths is a temperature in tenths of a degree.
//
// The challenge guarantees every input value has exactly one fractional digit
// (01-definition.md, README.md:422), so the whole problem lives on an integer
// grid and never needs float64.
//
// It is NOT chosen because float64 accumulation loses the answer: measured, a
// float64 sum over one station's share of a billion rows drifts by ~9.2e-07 and
// still recovers the exact tenths (TestFloat64AccumulationDriftIsSmall). It is
// chosen because it is exact by construction rather than by a margin that
// depends on the data, and because it makes the two rounding traps in
// 01-definition.md -- ties toward positive infinity, and "-0.0" -- unreachable
// rather than merely unlikely.
type Tenths int64

// Temperature bounds from README.md:422, inclusive: every legal value is a whole
// number of tenths in [-999, 999].
const (
	MinTenths Tenths = -999
	MaxTenths Tenths = 999
)

// RoundToTenths rounds v to the nearest tenth with ties going toward positive
// infinity, matching the challenge's required rounding direction (README.md:426)
// and Java's Math.round, which is floor(x+0.5).
//
// Go's math.Round rounds half away from zero and disagrees on every negative
// tie: math.Round(-24.5) is -25, floor(-24.5+0.5) is -24. Using math.Round here
// would corrupt one station in roughly every data set.
func RoundToTenths(v float64) Tenths {
	return Tenths(math.Floor(v*10.0 + 0.5))
}

// AppendTenths appends t as a decimal with exactly one fractional digit.
//
// The sign is emitted separately and the magnitude formatted unsigned, so "-0.0"
// cannot be produced. Formatting the equivalent float64 with %.1f would print
// "-0.0" for any small negative value, which no correct output ever contains.
//
// The whole part is not assumed to fit in two digits. It always does for legal
// data, but an earlier version encoded that assumption and turned an
// out-of-range value into unflagged garbage (1000 tenths printed as ":0.0"),
// which is the worst possible failure for a reference implementation.
func AppendTenths(dst []byte, t Tenths) []byte {
	if t < 0 {
		dst = append(dst, '-')
		t = -t
	}
	dst = strconv.AppendInt(dst, int64(t)/10, 10)
	return append(dst, '.', byte('0'+int64(t)%10))
}

// Mean returns the station mean in tenths, reproducing the reference
// implementation's arithmetic operation for operation
// (CalculateAverage_baseline.java:88 then :41):
//
//	round( ( round(sum) / count ) )
//
// The inner round is already applied: sumTenths is the exact one-decimal sum,
// which is what Math.round(sum*10.0) is trying to recover from a drifting
// float64 accumulator. The remaining float64 steps are kept in the same order
// and the same precision as the Java chain so the last digit agrees.
func Mean(sumTenths Tenths, count int64) Tenths {
	sum := float64(sumTenths) / 10.0
	return RoundToTenths(sum / float64(count))
}

// Accumulator holds one station's running aggregate, entirely in integer tenths.
type Accumulator struct {
	Min, Max Tenths
	Sum      Tenths
	Count    int64
}

// WriteResult emits the challenge's output line: a Java TreeMap.toString of
// name=min/mean/max, sorted by name, followed by a newline
// (01-definition.md, CalculateAverage_baseline.java:105).
func WriteResult(w io.Writer, stations map[string]*Accumulator) error {
	names := make([]string, 0, len(stations))
	for n := range stations {
		names = append(names, n)
	}
	sort.Strings(names)

	bw := bufio.NewWriterSize(w, 1<<20)
	bw.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			bw.WriteString(", ")
		}
		a := stations[n]
		buf := make([]byte, 0, 64)
		buf = append(buf, n...)
		buf = append(buf, '=')
		buf = AppendTenths(buf, a.Min)
		buf = append(buf, '/')
		buf = AppendTenths(buf, Mean(a.Sum, a.Count))
		buf = append(buf, '/')
		buf = AppendTenths(buf, a.Max)
		bw.Write(buf)
	}
	bw.WriteString("}\n")
	return bw.Flush()
}
