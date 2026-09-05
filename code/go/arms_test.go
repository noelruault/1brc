package main

import "testing"

// allFoldArms is every value -fold accepts, and the differential tests iterate it rather than a hand-picked subset.
// Arms are ADDED here and never replaced: a kernel that loses on this machine can win on another register file or cache hierarchy, so the losers stay shipped behind their flag and the lab suite re-ranks them elsewhere.
var allFoldArms = []struct {
	name string
	kind foldKind
}{
	{"slice", foldSlice},
	{"hash", foldHash},
	{"ptr", foldPtr},
	{"both", foldBoth},
	{"lanes", foldLanes},
	{"lanes4", foldLanes4},
}

// TestEveryFoldArmIsExercised is the guard that keeps the registry honest in both directions.
// Adding a foldKind without listing it here fails on the count; removing one that -fold still accepts fails on the parse.
func TestEveryFoldArmIsExercised(t *testing.T) {
	if len(allFoldArms) != int(foldKindCount) {
		t.Fatalf("%d fold arms registered, %d foldKind values exist: a -fold arm was added or removed without updating allFoldArms, so it is not in the differential test", len(allFoldArms), foldKindCount)
	}
	seen := map[foldKind]string{}
	for _, arm := range allFoldArms {
		got, err := foldMode(arm.name)
		if err != nil {
			t.Fatalf("-fold %q no longer parses: an arm was deleted, and arms are only ever added here", arm.name)
		}
		if got != arm.kind {
			t.Fatalf("-fold %q parses to %d, registry says %d", arm.name, got, arm.kind)
		}
		if prev, dup := seen[got]; dup {
			t.Fatalf("-fold %q and %q both map to kind %d", prev, arm.name, got)
		}
		seen[got] = arm.name
	}
}

// TestEveryFoldArmAgreesOnTheSameInput runs the full registry over the shapes that have historically split one arm from another, so a new arm meets every one of them on the day it lands.
// The corpus is deliberately the awkward cases: a name containing the separator is the divergence three shipped bugs shared, and no generated corpus can express it.
func TestEveryFoldArmAgreesOnTheSameInput(t *testing.T) {
	pad := "Pad;1.0\n"
	for _, tc := range []struct{ name, body string }{
		{"separator inside the name", pad + "Ab;cd;1.0\n" + pad},
		{"single row", "Hamburg;12.3\n"},
		{"negative and positive", pad + "A;-99.9\nA;99.9\nA;0.0\n" + pad},
		{"names either side of the eight-byte hash", pad + "a;1.0\nabcdefg;2.0\nabcdefgh;3.0\nabcdefghi;4.0\n" + pad},
		{"two names sharing all eight hashed bytes", pad + "AbcdefgH1;1.0\nAbcdefgH2;-1.0\nAbcdefgH1;3.0\n" + pad},
		{"repeated station", pad + "Zz;1.0\nZz;2.0\nZz;3.0\nZz;-4.0\n" + pad},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.body)
			want, wantSize, wantErr := foldArm(t, foldSlice, data)
			for _, arm := range allFoldArms {
				got, gotSize, gotErr := foldArm(t, arm.kind, data)
				if (gotErr == nil) != (wantErr == nil) {
					t.Fatalf("-fold %s: err %v, incumbent err %v", arm.name, gotErr, wantErr)
				}
				if gotErr != nil && gotErr.Error() != wantErr.Error() {
					t.Fatalf("-fold %s: err %q, incumbent err %q", arm.name, gotErr, wantErr)
				}
				if got != want {
					t.Fatalf("-fold %s:\n got %q\nwant %q", arm.name, got, want)
				}
				if gotSize != wantSize {
					t.Fatalf("-fold %s used %d buckets, the incumbent used %d: the two hashed the same name differently", arm.name, gotSize, wantSize)
				}
			}
			if wantErr == nil {
				if ref := referenceOutput(t, tc.body); ref != want {
					t.Fatalf("every arm agreed on %q and the reference says %q", want, ref)
				}
			}
		})
	}
}
