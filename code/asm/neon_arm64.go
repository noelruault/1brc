package asm

// The kernels below are implemented in neon_arm64.s.
// The whole study targets this machine (M5 Pro, darwin/arm64, spec.md:14), so the package deliberately has no other-architecture build.

// NEONIndexSemicolon returns the index of the first ';' in b, or -1, scanning 16 bytes per compare.
func NEONIndexSemicolon(b []byte) int

// neonTransferProbe reads the first 16 bytes of b and returns the narrowed compare mask. It requires len(b) >= 16.
func neonTransferProbe(b []byte) uint64
