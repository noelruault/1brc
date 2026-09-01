package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unsafe"

	gen "github.com/noelruault/research/1brc/code/gen"
)

type entry struct {
	key      []byte
	min, max int32
	sum      int64
	count    int32
}

// qentry is H-13's quotiented bucket, 32 bytes against entry's 48: the name's first 8 bytes sit inline instead of a 24-byte slice header, so a probe reads a third fewer bytes and the common compare is one word rather than a call into memequal.
//
// nlen holds len(name)+1 so that a zeroed bucket means EMPTY and a legal empty name still occupies one; word alone cannot say it, because a name of three bytes and the same name padded with NULs mask to the same word.
// ord indexes t.keys and is only read for names longer than 8 bytes, which is where word stops being the whole key: 34.1% of the 413 official rows (141 of 413 names exceed 8 bytes).
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
	_ = int16(gen.MaxTenths)
	_ = int16(gen.MinTenths)
)

// table is per-shard open addressing with linear probing: no lock, no atomic, no sharing, merged only when its shard is done.
//
// hashes is H5 (03-technique-recon.md:63) and q is H-13; keys is dense, one slot per STATION rather than per bucket, so the quotiented layout costs no extra zeroing at startup.
// The mode is one branch per row, taken identically in every layout, so the comparison between them stays fair.
type table struct {
	hashes []uint64
	e      []entry
	q      []qentry
	keys   [][]byte
	mask   uint64
	size   int
}

type tableKind int

const (
	tableCombined tableKind = iota
	tableSplit
	tableQuot
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
	}
	return 0, fmt.Errorf("unknown -table %q, want combined, split or quot", s)
}

func newTable(bits int, kind tableKind) *table {
	t := &table{mask: 1<<bits - 1}
	switch kind {
	case tableQuot:
		t.q = make([]qentry, 1<<bits)
		t.keys = make([][]byte, 0, 1024)
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
func (t *table) drain(into map[string]*gen.Accumulator) {
	for i := range t.q {
		e := &t.q[i]
		if e.nlen == 0 {
			continue
		}
		key := t.keys[e.ord]
		a := into[string(key)]
		if a == nil {
			into[string(key)] = &gen.Accumulator{Min: gen.Tenths(e.min), Max: gen.Tenths(e.max), Sum: gen.Tenths(e.sum), Count: int64(e.count)}
			continue
		}
		if gen.Tenths(e.min) < a.Min {
			a.Min = gen.Tenths(e.min)
		}
		if gen.Tenths(e.max) > a.Max {
			a.Max = gen.Tenths(e.max)
		}
		a.Sum += gen.Tenths(e.sum)
		a.Count += int64(e.count)
	}
	for i := range t.e {
		e := &t.e[i]
		if e.key == nil {
			continue
		}
		a := into[string(e.key)]
		if a == nil {
			into[string(e.key)] = &gen.Accumulator{Min: gen.Tenths(e.min), Max: gen.Tenths(e.max), Sum: gen.Tenths(e.sum), Count: int64(e.count)}
			continue
		}
		if gen.Tenths(e.min) < a.Min {
			a.Min = gen.Tenths(e.min)
		}
		if gen.Tenths(e.max) > a.Max {
			a.Max = gen.Tenths(e.max)
		}
		a.Sum += gen.Tenths(e.sum)
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
