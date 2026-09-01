// Command 1brc aggregates a 1BRC measurements file into min/mean/max per station.
//
// This is the skeleton: one goroutine, bufio, a Go map. It exists so the correctness gate and the benchmark harness have something to run before any of it is fast.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	gen "github.com/noelruault/research/1brc/code/gen"
)

func main() {
	in := flag.String("in", "measurements.txt", "measurements file to aggregate")
	flag.Parse()

	if err := run(*in, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "1brc:", err)
		os.Exit(1)
	}
}

func run(path string, out io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	stations, err := aggregate(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return err
	}
	return gen.WriteResult(out, stations)
}

// aggregate folds the file into per-station accumulators, matching gen.Aggregate's semantics exactly (first ';' is the separator, out-of-range temperatures are an error with a line number) because gen produced the expected output this binary is byte-compared against.
//
// Rounding and output formatting stay in gen so the output contract has one definition rather than two that can drift.
func aggregate(r *bufio.Reader) (map[string]*gen.Accumulator, error) {
	stations := make(map[string]*gen.Accumulator, 1<<14)
	line := 0
	for {
		b, err := r.ReadSlice('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("line %d: %w", line+1, err)
		}
		if len(b) == 0 {
			return stations, nil
		}
		line++
		row := b
		if n := len(row); n > 0 && row[n-1] == '\n' {
			row = row[:n-1]
		}
		if len(row) == 0 {
			continue
		}

		sep := bytes.IndexByte(row, ';')
		if sep < 0 {
			return nil, fmt.Errorf("line %d: no ';' separator", line)
		}
		temp, ok := gen.ParseTenths(row[sep+1:])
		if !ok {
			return nil, fmt.Errorf("line %d: %q is not a one-decimal temperature", line, row[sep+1:])
		}
		if temp < gen.MinTenths || temp > gen.MaxTenths {
			return nil, fmt.Errorf("line %d: %q is outside [-99.9, 99.9]", line, row[sep+1:])
		}

		name := row[:sep]
		if a := stations[string(name)]; a != nil {
			if temp < a.Min {
				a.Min = temp
			}
			if temp > a.Max {
				a.Max = temp
			}
			a.Sum += temp
			a.Count++
			continue
		}
		stations[string(name)] = &gen.Accumulator{Min: temp, Max: temp, Sum: temp, Count: 1}
	}
}
