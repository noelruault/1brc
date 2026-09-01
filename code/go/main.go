// Command 1brc aggregates a 1BRC measurements file into min/mean/max per station.
//
// v1: more goroutines than cores (E-17), each owning byte ranges of the file, each folding into its own open-addressing table, merged once at the end. The strategy flags exist because 03-technique-recon.md left four hypotheses open and this binary is where they are measured; the defaults are what the measurements picked.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"

	gen "github.com/noelruault/research/1brc/code/gen"
)

func main() {
	in := flag.String("in", "measurements.txt", "measurements file to aggregate")
	cfg := config{}
	flag.IntVar(&cfg.Workers, "workers", defaultWorkers(), "parallel readers/aggregators")
	flag.IntVar(&cfg.BufKiB, "buf", defaultBufKiB, "per-worker read buffer, KiB (pread only)")
	flag.IntVar(&cfg.SegKiB, "seg", 2048, "segment claimed per turn, KiB (-split cursor only)")
	flag.IntVar(&cfg.Bits, "bits", 17, "log2 of the per-worker table's bucket count")
	flag.BoolVar(&cfg.NoCache, "nocache", true, "set F_NOCACHE so reads bypass the page cache (pread only)")
	flag.StringVar(&cfg.Split, "split", "static", "work distribution: static | cursor (H1)")
	flag.StringVar(&cfg.Table, "table", "combined", "table layout: combined | split (H5)")
	flag.StringVar(&cfg.IO, "io", "pread", "reader: pread | mmap (H7)")
	flag.StringVar(&cfg.Parse, "parse", defaultParse, "temperature parse and format check: branchless | scalar (H3) | word (E-25)")
	flag.StringVar(&cfg.Kernel, "kernel", "row", "tokenizer: row | batch-swar | batch-neon (go-v2-kernels)")
	flag.StringVar(&cfg.Fold, "fold", defaultFold, "row loop: slice | hash | ptr | both (queue items 1 and 5)")
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

// defaultFold is the incumbent row loop, unchanged, and it stays that until an arm beats it disjointly: the other three values exist to be measured, not to be defaults in waiting.
const defaultFold = "slice"

func run(path string, cfg config, out io.Writer) error {
	stations, err := aggregateFile(path, cfg)
	if err != nil {
		return err
	}
	// Buffered: WriteResult emits one small write per station and the 10k case has ten thousand of them.
	w := bufio.NewWriterSize(out, 1<<20)
	if err := gen.WriteResult(w, stations); err != nil {
		return err
	}
	return w.Flush()
}
