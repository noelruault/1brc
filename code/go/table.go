package main

import (
	"bytes"
	"encoding/binary"
	"fmt"

	gen "github.com/noelruault/research/1brc/code/gen"
)

type entry struct {
	key      []byte
	min, max int32
	sum      int64
	count    int32
}

// table is per-shard open addressing with linear probing: no lock, no atomic, no sharing, merged only when its shard is done.
//
// hashes is H5 (03-technique-recon.md:63): when it is non-nil the probe walks an array of 8-byte hashes instead of 48-byte entries, so the array a miss touches is a sixth of the size.
// The mode is one branch per row, taken identically in both layouts, so the comparison between them stays fair.
type table struct {
	hashes []uint64
	e      []entry
	mask   uint64
	size   int
}

func newTable(bits int, split bool) *table {
	t := &table{e: make([]entry, 1<<bits), mask: 1<<bits - 1}
	if split {
		t.hashes = make([]uint64, 1<<bits)
	}
	return t
}

// update folds one reading into the table and reports false when the table is FULL, which is the one way a linear probe can fail: with no empty slot the probe loop never ends, and a hang is a worse failure than an error.
// h indexes, and in split mode is also stored with its low bit forced so that zero can mean "empty"; two hashes that differ only in that bit are separated by the full key compare anyway.
func (t *table) update(h uint64, name []byte, v int32) bool {
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
func (t *table) fold(data []byte, k kernel, pk parseKind, base int64) error {
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
		pos, err = t.foldRows(data, pk, base)
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
		if !t.update(hashWord(binary.LittleEndian.Uint64(data[pos:]), sep), name, v) {
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
		if !t.update(hashName(name), name, v) {
			return t.fullError(base + int64(pos))
		}
		pos += sep + 1 + next
	}
	return nil
}

// drain folds this shard's entries into the shared result map. It runs once per shard, so it is allowed to be obvious.
func (t *table) drain(into map[string]*gen.Accumulator) {
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
	return fmt.Errorf("byte %d: all %d table buckets are occupied; raise -bits", offset, len(t.e))
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
