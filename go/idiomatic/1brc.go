// Command 1brc aggregates a 1BRC measurements file into per-station min/mean/max: go run 1brc.go -in measurements.txt
//
// This is the idiomatic tier: the standard library only, `syscall` included, so the rows are walked as slices and the read still sets darwin's F_NOCACHE.
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
	"sort"
	"strconv"
	"sync"
	"syscall"
)

// maxRow is the longest row the fast path assumes fits: 100 name bytes, ';', "-99.9", '\n', and headroom.
// The fast path runs only while a whole row plus its 8-byte over-fetch is inside the buffer, which is what lets every load below be unconditional.
const maxRow = 128

// fNoCache is darwin's F_NOCACHE. The file is 53.5% of RAM, so the page cache evicts its head while its tail is read and an uncached parallel read beats it.
const fNoCache = 48

// bucketBits is measured, not round: 1<<17 buckets hold the 413 official stations at a 0.3% load factor, and the 10k set at 7.6%.
const bucketBits = 17

// readChunk is a measured minimum rather than a round number: 1 MiB won disjoint against 512 KiB, 2 MiB and 4 MiB, and the whole gain is kernel-side.
const readChunk = 1 << 20

// workerRatio oversubscribes the cores on purpose: a worker blocked in its read leaves its core to another's fold, which measured 7.5% under one-per-core.
const workerRatio = 4

func main() {
	in := flag.String("in", "measurements.txt", "measurements file to aggregate")
	flag.Parse()

	stations, err := aggregate(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "1brc:", err)
		os.Exit(1)
	}
	w := bufio.NewWriterSize(os.Stdout, 1<<20)
	writeResult(w, stations)
	if err := w.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "1brc:", err)
		os.Exit(1)
	}
}

// ---- the aggregate ----

type accumulator struct {
	min, max int32
	sum      int64
	count    int32
}

// aggregate divides the file by BYTE offset, never by row. A range owns exactly the rows whose FIRST byte falls inside it, so a range that does not start at 0 skips the partial row it lands in and every range finishes the row straddling its end.
func aggregate(path string) (map[string]*accumulator, error) {
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
		return map[string]*accumulator{}, nil
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], size-1); err != nil {
		return nil, err
	}
	if last[0] != '\n' {
		return nil, fmt.Errorf("input does not end with a newline")
	}
	if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), uintptr(fNoCache), 1); e != 0 {
		return nil, fmt.Errorf("fcntl F_NOCACHE: %w", e)
	}

	workers := runtime.NumCPU() * workerRatio / 3
	span := (size + int64(workers) - 1) / int64(workers)
	tables := make([]*table, workers)
	errs := make([]error, workers)

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			lo := int64(w) * span
			if lo >= size {
				return
			}
			t := newTable()
			tables[w] = t
			errs[w] = t.foldRange(f, lo, min(lo+span, size), size, make([]byte, readChunk))
		}(w)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	result := make(map[string]*accumulator, 1<<14)
	for _, t := range tables {
		if t != nil {
			t.drain(result)
		}
	}
	return result, nil
}

// ---- the read ----

// foldRange reads [lo,hi) in buffer-sized chunks, carrying the partial row at the end of each chunk into the front of the next, and folds the row straddling hi by reading up to maxRow past it.
//
// A range that does not start at 0 starts reading ONE BYTE EARLY. That byte is what separates "lo is mid-row, skip to the next boundary" from "lo IS a boundary, keep the row starting there"; without it one row per aligned boundary is silently dropped.
func (t *table) foldRange(f *os.File, lo, hi, size int64, buf []byte) error {
	readEnd := min(hi+maxRow, size)
	readStart := lo
	if lo > 0 {
		readStart = lo - 1
	}
	off, carry, first := readStart, 0, true
	for off < readEnd {
		want := min(int64(len(buf)-carry), readEnd-off)
		n, err := f.ReadAt(buf[carry:carry+int(want)], off)
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
			// A range shorter than one row contains no row START, so it owns nothing; without this it folds the next range's first row and that row is counted twice.
			if base+int64(from) >= hi {
				return nil
			}
		}
		first = false

		if base+int64(avail) >= hi {
			// The straddling row may still be incomplete when the buffer is no bigger than the range: fall through, fold the whole rows, and read the rest of it next time round.
			if end, ok := rangeEnd(buf[:avail], base, hi, from); ok {
				return t.fold(buf[from:end], base+int64(from))
			}
		}
		nl := bytes.LastIndexByte(buf[:avail], '\n')
		if nl < from {
			return fmt.Errorf("byte %d: row longer than the %d-byte buffer", base, len(buf))
		}
		if err := t.fold(buf[from:nl+1], base+int64(from)); err != nil {
			return err
		}
		carry = copy(buf, buf[nl+1:avail])
	}
	return fmt.Errorf("byte %d: the row crossing the end of the range is longer than %d bytes", hi, maxRow)
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

// ---- the fold ----

// fold re-slices at every row, so every index is bounds-checked by the compiler rather than by the loop.
// It stops maxRow short of the end so the 8-byte over-fetch below stays inside the buffer; foldTail closes the rest without ever over-reading.
func (t *table) fold(data []byte, base int64) error {
	pos := 0
	for pos+maxRow <= len(data) {
		sep, semi := indexDelim(data[pos:])
		if sep < 0 || !semi {
			return rowError(base+int64(pos), data[pos:])
		}
		if pos+sep+9 > len(data) {
			break
		}
		v, next, ok := parseTemp(binary.LittleEndian.Uint64(data[pos+sep+1:]))
		if !ok || uint32(v+999) > 1998 {
			return rowError(base+int64(pos), data[pos:])
		}
		name := data[pos : pos+sep]
		kw := maskWord(binary.LittleEndian.Uint64(data[pos:]), sep)
		if !t.update(mixWord(kw), name, v) {
			return t.fullError(base + int64(pos))
		}
		pos += sep + 1 + next
	}
	return t.foldTail(data, pos, base)
}

// foldTail closes the rows the fast path stopped short of, with the scalar parse that never over-reads.
func (t *table) foldTail(data []byte, pos int, base int64) error {
	for pos < len(data) {
		sep, semi := indexDelim(data[pos:])
		if sep < 0 || !semi {
			return rowError(base+int64(pos), data[pos:])
		}
		v, next, ok := parseTempScalar(data[pos+sep+1:])
		if !ok || uint32(v+999) > 1998 {
			return rowError(base+int64(pos), data[pos:])
		}
		name := data[pos : pos+sep]
		if !t.update(mixWord(maskName(name)), name, v) {
			return t.fullError(base + int64(pos))
		}
		pos += sep + 1 + next
	}
	return nil
}

// ---- the kernels ----

const (
	semicolonBroadcast = 0x3B3B3B3B3B3B3B3B
	newlineBroadcast   = 0x0A0A0A0A0A0A0A0A
	swarLow            = 0x0101010101010101
	swarHigh           = 0x8080808080808080
)

// indexDelim finds the first ';' or '\n' eight bytes at a time and reports which one it found.
//
// A borrow chain can set a lane's high bit ABOVE a match but never below one, so the lowest set bit is always the first match.
// Both needles are scanned, not just ';', because a row with no separator would otherwise run its name into the NEXT row and fold a station that does not exist; the reference rejects that row, so this has to as well.
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

const (
	parseDotBits     = 0x10101000
	parseDigitsHi    = 0x0F000F0F00
	parseMagic       = 0x640a0001
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
// A lane is a digit exactly when its high nibble is 3 and its low nibble is under 10, which is why both halves are needed: ':' passes the high-nibble test and 'b' passes the low-nibble one.
func nonDigitMask(w uint64) uint64 {
	return (w^digitThrees)&digitHighNibbles | ((w&digitLowNibbles)+digitSixes)&digitHighNibbles
}

// parseTemp reads a one-decimal temperature out of the word the field starts in, rejecting any other shape.
//
// All four rejections are ORed into one accumulator so none of them can branch, and each is load-bearing alone: only the count rejects "100.0", only the sign byte "\x005.0", only the digit lanes "1x.5", only the '\n' lane "1.5x", only the '.' lane "1-5".
// The dot guard is the one branch kept, because a negative shift would panic.
func parseTemp(w uint64) (tenths int32, next int, ok bool) {
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

// parseTempScalar is the tail's parse: branchy, but it never over-reads, which is what lets it close a buffer's last rows.
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

// maskWord keeps the bytes after the name out of the word the row was read in. A shift of 64 is zero in Go, so an empty name masks to zero rather than reading anything.
func maskWord(w uint64, nameLen int) uint64 {
	return w & (^uint64(0) >> ((8 - uint(min(nameLen, 8))) * 8))
}

// mixWord turns a masked key word into a bucket index.
func mixWord(w uint64) uint64 { return (w ^ w>>29) * hashMultiplier }

// maskName is maskWord for the tail, which holds the name but not the word, and it MUST agree with maskWord byte for byte: a station hashed two ways would occupy two buckets and split its own aggregate.
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

// ---- the table ----

type entry struct {
	key      []byte
	min, max int32
	sum      int64
	count    int32
}

// table is per-shard open addressing with linear probing: no lock, no atomic, no sharing, merged only once its shard is done.
type table struct {
	e    []entry
	mask uint64
	size int
}

func newTable() *table {
	return &table{e: make([]entry, 1<<bucketBits), mask: 1<<bucketBits - 1}
}

// update folds one reading into the table and reports false when it is FULL, which is the one way a linear probe can fail: with no empty slot the probe never ends, and a hang is a worse failure than an error.
func (t *table) update(h uint64, name []byte, v int32) bool {
	if t.size == len(t.e) {
		return false
	}
	for i := h & t.mask; ; i = (i + 1) & t.mask {
		e := &t.e[i]
		if e.key == nil {
			key := make([]byte, len(name))
			copy(key, name)
			*e = entry{key: key, min: v, max: v, sum: int64(v), count: 1}
			t.size++
			return true
		}
		if bytes.Equal(e.key, name) {
			// Branchless min and max: d>>31 is all ones exactly when d is negative, so each add is either the whole difference or nothing.
			dmin := v - e.min
			e.min += dmin & (dmin >> 31)
			dmax := v - e.max
			e.max += dmax &^ (dmax >> 31)
			e.sum += int64(v)
			e.count++
			return true
		}
	}
}

// drain folds this shard's entries into the shared result. It runs once per shard, so it is allowed to be obvious.
func (t *table) drain(into map[string]*accumulator) {
	for i := range t.e {
		e := &t.e[i]
		if e.key == nil {
			continue
		}
		a := into[string(e.key)]
		if a == nil {
			into[string(e.key)] = &accumulator{min: e.min, max: e.max, sum: e.sum, count: e.count}
			continue
		}
		if e.min < a.min {
			a.min = e.min
		}
		if e.max > a.max {
			a.max = e.max
		}
		a.sum += e.sum
		a.count += e.count
	}
}

func (t *table) fullError(offset int64) error {
	return fmt.Errorf("byte %d: all %d table buckets are occupied", offset, len(t.e))
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

// ---- the output ----

// writeResult prints name=min/mean/max sorted by name, which is the reference's own format.
func writeResult(w io.Writer, stations map[string]*accumulator) {
	names := make([]string, 0, len(stations))
	for n := range stations {
		names = append(names, n)
	}
	sort.Strings(names)

	buf := make([]byte, 0, 64)
	io.WriteString(w, "{")
	for i, n := range names {
		if i > 0 {
			io.WriteString(w, ", ")
		}
		a := stations[n]
		buf = append(buf[:0], n...)
		buf = append(buf, '=')
		buf = appendTenths(buf, int64(a.min))
		buf = append(buf, '/')
		buf = appendTenths(buf, mean(a.sum, int64(a.count)))
		buf = append(buf, '/')
		buf = appendTenths(buf, int64(a.max))
		w.Write(buf)
	}
	io.WriteString(w, "}\n")
}

// mean reproduces the reference's arithmetic operation for operation: round(round(sum)/count), with the inner round already applied because the sum is exact in tenths.
func mean(sumTenths, count int64) int64 {
	return int64(math.Floor(float64(sumTenths)/10.0/float64(count)*10.0 + 0.5))
}

// appendTenths appends t as a decimal with exactly one fractional digit.
//
// The sign is emitted separately and the magnitude formatted unsigned, so "-0.0" cannot be produced; %.1f on the equivalent float64 would print it for any small negative value.
// Rounding runs toward positive infinity, matching Java's Math.round, which is floor(x+0.5): Go's math.Round rounds half away from zero and disagrees on every negative tie.
func appendTenths(dst []byte, t int64) []byte {
	if t < 0 {
		dst = append(dst, '-')
		t = -t
	}
	dst = strconv.AppendInt(dst, t/10, 10)
	return append(dst, '.', byte('0'+t%10))
}
