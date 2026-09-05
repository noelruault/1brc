package main

import (
	"math/rand"
	"testing"

	gen "github.com/noelruault/research/1brc/code/gen"
)

// These three split (*table).update into the parts a perfect hash would and would not remove.
// PARKED.md P-03's revive trigger asks for the probe and the key compare to be attributed separately, which no -cpuprofile run can do because they inline into one symbol.
// DirectIndex is the CEILING and not a proposal: it deletes the probe, the compare AND the hash, so a real perfect hash — which still hashes and still needs a fallback branch — must come in under whatever gap it shows.

func attribCorpus() (keys [][]byte, hashes []uint64, words []uint64, slot []int, t *table, idx []int) {
	names := gen.Official413()
	keys = make([][]byte, len(names))
	hashes = make([]uint64, len(names))
	words = make([]uint64, len(names))
	slot = make([]int, len(names))
	t = newTable(17, tableCombined)
	for i, n := range names {
		keys[i] = []byte(n.Name)
		words[i] = maskName(keys[i])
		hashes[i] = mixWord(words[i])
		t.update(hashes[i], words[i], keys[i], 0)
	}
	for i := range keys {
		j := hashes[i] & t.mask
		for !bytesEqualKey(t.e[j].key, keys[i]) {
			j = (j + 1) & t.mask
		}
		slot[i] = int(j)
	}
	// One fixed access sequence for all three variants, so their difference is the mechanism and not the order.
	r := rand.New(rand.NewSource(1))
	idx = make([]int, 1<<16)
	for i := range idx {
		idx[i] = r.Intn(len(names))
	}
	return
}

func bytesEqualKey(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func BenchmarkUpdateFull(b *testing.B) {
	keys, hashes, words, _, t, idx := attribCorpus()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		k := idx[i&(len(idx)-1)]
		t.update(hashes[k], words[k], keys[k], int32(k%999)-499)
	}
}

// BenchmarkUpdateNoCompare keeps the hash, the index and the empty-slot check and drops only bytes.Equal, so the gap to Full is what the key compare costs.
func BenchmarkUpdateNoCompare(b *testing.B) {
	_, hashes, _, _, t, idx := attribCorpus()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		k := idx[i&(len(idx)-1)]
		j := hashes[k] & t.mask
		for t.e[j].key == nil {
			j = (j + 1) & t.mask
		}
		t.merge(int(j), int32(k%999)-499)
	}
}

// BenchmarkUpdateDirectIndex is the ceiling: slot known, so no hash, no probe and no compare, leaving only the min/max/sum/count arithmetic.
func BenchmarkUpdateDirectIndex(b *testing.B) {
	_, _, _, slot, t, idx := attribCorpus()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		k := idx[i&(len(idx)-1)]
		t.merge(slot[k], int32(k%999)-499)
	}
}

// TestAttributionSlotsAreTheSameEntries pins the invariant the two cheap variants rest on: the slot they fold into is the one update would have chosen.
// Without this a gap between the benchmarks could be them folding into different entries rather than costing different amounts.
func TestAttributionSlotsAreTheSameEntries(t *testing.T) {
	keys, hashes, words, slot, tab, _ := attribCorpus()
	for i := range keys {
		if got := tab.e[slot[i]].key; !bytesEqualKey(got, keys[i]) {
			t.Fatalf("slot %d holds %q, want %q", slot[i], got, keys[i])
		}
		before := tab.e[slot[i]].count
		tab.update(hashes[i], words[i], keys[i], 42)
		if tab.e[slot[i]].count != before+1 {
			t.Fatalf("update for %q did not land on slot %d", keys[i], slot[i])
		}
	}
}
