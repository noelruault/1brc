// Command reference is the trivially-correct 1BRC aggregator. It defines the
// expected output every fast implementation is byte-compared against.
//
//	go run ./cmd/reference -in /path/measurements-10m.txt > expected-10m.out
package main

import (
	"bufio"
	"flag"
	"log"
	"os"

	gen "github.com/noelruault/research/1brc/code/gen"
)

func main() {
	in := flag.String("in", "", "measurements file to aggregate (required)")
	flag.Parse()

	if *in == "" {
		log.Fatal("-in is required")
	}
	f, err := os.Open(*in)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	stations, err := gen.Aggregate(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		log.Fatal(err)
	}
	if err := gen.WriteResult(os.Stdout, stations); err != nil {
		log.Fatal(err)
	}
}
