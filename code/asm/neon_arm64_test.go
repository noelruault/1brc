package asm

import (
	"os"
	"syscall"
	"testing"
)

// TestNEONIndexSemicolonStopsAtTheSliceEnd places the input so its last byte is the last byte of a mapped page, with the next page PROT_NONE.
// A 16-byte load that runs past the slice segfaults here, which no amount of fuzzing over a slice of a larger buffer can catch.
func TestNEONIndexSemicolonStopsAtTheSliceEnd(t *testing.T) {
	page := os.Getpagesize()
	m, err := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		t.Fatalf("mmap: %v", err)
	}
	defer syscall.Munmap(m)
	if err := syscall.Mprotect(m[page:], syscall.PROT_NONE); err != nil {
		t.Fatalf("mprotect: %v", err)
	}

	for n := 1; n <= 40; n++ {
		b := m[page-n : page]
		for i := range b {
			b[i] = 'x'
		}
		if got := NEONIndexSemicolon(b); got != -1 {
			t.Fatalf("n=%d: got %d, want -1", n, got)
		}
		for sep := 0; sep < n; sep++ {
			b[sep] = ';'
			if got := NEONIndexSemicolon(b); got != sep {
				t.Fatalf("n=%d sep=%d: got %d", n, sep, got)
			}
			b[sep] = 'x'
		}
	}
}
