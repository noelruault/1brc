package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunModesAgreeOnTheSameFile pins the two things a floor number depends on: that every
// byte is read, and that the newline tally survives a chunk size smaller than the file.
func TestRunModesAgreeOnTheSameFile(t *testing.T) {
	body := strings.Repeat("Hamburg;12.0\n", 1000) + "Washington, D.C.;-9.9\n"
	path := filepath.Join(t.TempDir(), "measurements.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	wantBytes, wantLines := int64(len(body)), int64(1001)

	for _, bs := range []int{7, 64, 4096, 1 << 20} {
		for _, mode := range []string{"read", "count", "scan", "mmap"} {
			n, lines, _, err := run(path, mode, bs, false, 1)
			if err != nil {
				t.Fatalf("mode=%s bs=%d: %v", mode, bs, err)
			}
			if n != wantBytes {
				t.Errorf("mode=%s bs=%d: read %d bytes, want %d", mode, bs, n, wantBytes)
			}
			if mode != "read" && lines != wantLines {
				t.Errorf("mode=%s bs=%d: counted %d lines, want %d", mode, bs, lines, wantLines)
			}
		}
	}
}

func TestRunRejectsUnknownMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := run(path, "swar", 4096, false, 1); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

// TestRunNoCacheReadsEveryByte guards the fcntl path: a wrong F_NOCACHE constant or a
// darwin change would fail the syscall, and a silently short read would be a fake floor.
func TestRunNoCacheReadsEveryByte(t *testing.T) {
	body := strings.Repeat("Abha;-24.7\n", 5000)
	path := filepath.Join(t.TempDir(), "nocache.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	n, lines, _, err := run(path, "count", 16384, true, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(body)) || lines != 5000 {
		t.Fatalf("nocache read %d bytes / %d lines, want %d / 5000", n, lines, len(body))
	}
}

// TestParallelScanReadsEveryByteExactlyOnce is the check the bandwidth number rests on: if the
// range split dropped or double-counted a byte, the reported GB/s would be a fiction.
func TestParallelScanReadsEveryByteExactlyOnce(t *testing.T) {
	body := strings.Repeat("Abéché;29.4\n", 3333) + "x;0.0\n"
	path := filepath.Join(t.TempDir(), "parallel.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{1, 2, 3, 7, 15, 64, 100000} {
		for _, bs := range []int{13, 512, 1 << 20} {
			n, lines, _, err := run(path, "count", bs, false, workers)
			if err != nil {
				t.Fatalf("workers=%d bs=%d: %v", workers, bs, err)
			}
			if n != int64(len(body)) || lines != 3334 {
				t.Errorf("workers=%d bs=%d: %d bytes / %d lines, want %d / 3334", workers, bs, n, lines, len(body))
			}
		}
	}
}

func TestRunRejectsBadWorkerCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.txt")
	if err := os.WriteFile(path, []byte("a;1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := run(path, "count", 4096, false, 0); err == nil {
		t.Fatal("workers=0 accepted")
	}
	if _, _, _, err := run(path, "scan", 4096, false, 4); err == nil {
		t.Fatal("scan accepted -workers 4, which it cannot honour")
	}
}

// TestMmapCountMatchesTheReadPath keeps the no-copy ceiling honest: the mapping is split on
// raw offsets, so a slice boundary landing on a newline must still be counted exactly once.
func TestMmapCountMatchesTheReadPath(t *testing.T) {
	body := strings.Repeat("Flores,  Petén;16.3\n", 4097)
	path := filepath.Join(t.TempDir(), "mmap.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{1, 2, 15, 4096, 99999} {
		n, lines, _, err := run(path, "mmap", 1<<20, false, workers)
		if err != nil {
			t.Fatalf("workers=%d: %v", workers, err)
		}
		if n != int64(len(body)) || lines != 4097 {
			t.Errorf("workers=%d: %d bytes / %d lines, want %d / 4097", workers, n, lines, len(body))
		}
	}
	if _, _, _, err := run(filepath.Join(t.TempDir(), "absent.txt"), "mmap", 1<<20, false, 1); err == nil {
		t.Fatal("mmap of a missing file succeeded")
	}
}
