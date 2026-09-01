// Command measurements writes a reproducible 1BRC measurement file.
//
//	go run ./cmd/measurements -rows 10000000 -out /path/measurements-10m.txt
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	gen "github.com/noelruault/research/1brc/code/gen"
)

func main() {
	rows := flag.Int64("rows", 10_000_000, "number of measurement rows to write")
	out := flag.String("out", "", "output file path (required; keep it outside the repo)")
	keyset := flag.String("keyset", "413", "station key set: 413 (official) or 10k (synthetic stress set)")
	seed := flag.Uint64("seed", 1, "PRNG seed; the same seed and keyset reproduce the file byte for byte")
	flag.Parse()

	if *out == "" {
		log.Fatal("-out is required")
	}

	var stations []gen.Station
	var stddev float64
	switch *keyset {
	case "413":
		stations, stddev = gen.Official413(), 10.0
	case "10k":
		stations, stddev = gen.Synthetic10k(), 7.0
	default:
		log.Fatalf("-keyset must be 413 or 10k, got %q", *keyset)
	}

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	start := time.Now()
	n, err := gen.Write(f, stations, *rows, stddev, *seed)
	if err != nil {
		f.Close()
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	elapsed := time.Since(start)
	fmt.Printf("rows=%d stations=%d stddev=%.1f seed=%d bytes=%d elapsed=%s rate=%.1f MB/s\n",
		*rows, len(stations), stddev, *seed, n, elapsed.Round(time.Millisecond),
		float64(n)/elapsed.Seconds()/(1<<20))
}
