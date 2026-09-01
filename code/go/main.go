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

	gen "github.com/noelruault/research/1brc/code/gen"
)

func main() {
	in := flag.String("in", "measurements.txt", "measurements file to aggregate")
	cfg := config{}
	flag.IntVar(&cfg.Workers, "workers", defaultWorkers(), "parallel readers/aggregators")
	flag.IntVar(&cfg.BufKiB, "buf", 4096, "per-worker read buffer, KiB (pread only)")
	flag.IntVar(&cfg.SegKiB, "seg", 2048, "segment claimed per turn, KiB (-split cursor only)")
	flag.IntVar(&cfg.Bits, "bits", 17, "log2 of the per-worker table's bucket count")
	flag.BoolVar(&cfg.NoCache, "nocache", true, "set F_NOCACHE so reads bypass the page cache (pread only)")
	flag.StringVar(&cfg.Split, "split", "static", "work distribution: static | cursor (H1)")
	flag.StringVar(&cfg.Table, "table", "combined", "table layout: combined | split (H5)")
	flag.StringVar(&cfg.IO, "io", "pread", "reader: pread | mmap (H7)")
	flag.StringVar(&cfg.Parse, "parse", "branchless", "temperature parse: branchless | scalar (H3)")
	flag.StringVar(&cfg.Kernel, "kernel", "row", "tokenizer: row | batch-swar | batch-neon (go-v2-kernels)")
	flag.BoolVar(&cfg.Madvise, "madvise", false, "MADV_WILLNEED the whole mapping first (-io mmap only, H7's rescue)")
	flag.Parse()

	if err := run(*in, cfg, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "1brc:", err)
		os.Exit(1)
	}
}

// defaultWorkers oversubscribes the cores on purpose: E-17 measured 20 workers on this 15-core machine at 7.5% under one-per-core, slot-corrected and disjoint, because a worker blocked in its read leaves its core to another's fold.
// The ratio is that one measurement generalised so the default still means something on other core counts, not a law; 30 workers also beat 15 and did not separate from 20, so the optimum is a plateau and this is a point inside it.
func defaultWorkers() int { return runtime.NumCPU() * 4 / 3 }

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
