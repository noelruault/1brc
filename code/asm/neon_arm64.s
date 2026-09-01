#include "textflag.h"

// arm64 has no PMOVMSKB, so a 16-byte compare result is narrowed with SHRN #4 over the 8x16-bit view:
// that packs each byte lane's 0x00/0xFF into one nibble of a 64-bit word, and ctz(mask)>>2 is the lane index.
// 03-technique-recon.md, technique 2.

// func NEONIndexSemicolon(b []byte) int
TEXT ·NEONIndexSemicolon(SB), NOSPLIT, $0-32
	MOVD	b_base+0(FP), R0
	MOVD	b_len+8(FP), R1
	MOVD	R0, R6              // cursor
	ADD	R0, R1, R7          // end
	CMP	$16, R1
	BLO	tail                // fewer than 16 bytes: the vector load would read past the slice
	SUB	$16, R7, R8         // last address a 16-byte load may start at
	MOVD	$0x3B, R2
	VDUP	R2, V0.B16

window:
	VLD1	(R6), [V1.B16]
	VCMEQ	V0.B16, V1.B16, V2.B16
	VSHRN	$4, V2.H8, V3.B8
	VMOV	V3.D[0], R9
	CBNZ	R9, found
	ADD	$16, R6, R6
	CMP	R8, R6
	BLS	window
	B	tail

found:
	RBIT	R9, R10
	CLZ	R10, R11
	LSR	$2, R11, R11
	SUB	R0, R6, R12
	ADD	R12, R11, R11
	MOVD	R11, ret+24(FP)
	RET

tail:
	CMP	R7, R6
	BHS	notfound
	MOVBU	(R6), R9
	CMP	$0x3B, R9
	BEQ	tailfound
	ADD	$1, R6, R6
	B	tail

tailfound:
	SUB	R0, R6, R11
	MOVD	R11, ret+24(FP)
	RET

notfound:
	MOVD	$-1, R11
	MOVD	R11, ret+24(FP)
	RET

// func neonTransferProbe(b []byte) uint64
// One 16-byte compare narrowed and moved to a general register, and nothing else.
// It measures the floor any NEON kernel pays per row for getting its result out of the vector unit.
TEXT ·neonTransferProbe(SB), NOSPLIT, $0-32
	MOVD	b_base+0(FP), R0
	MOVD	$0x3B, R2
	VDUP	R2, V0.B16
	VLD1	(R0), [V1.B16]
	VCMEQ	V0.B16, V1.B16, V2.B16
	VSHRN	$4, V2.H8, V3.B8
	VMOV	V3.D[0], R9
	MOVD	R9, ret+24(FP)
	RET

// func neonDelimMask32(b []byte) (semi, nl uint64)
// One 32-byte load answered for BOTH needles, gigatoken's shape: the vector-to-general transfer is
// amortised over every row in the window instead of paid once per row.
// The 0x40100401 syndrome (Go's own bytes.IndexByte idiom) gives lane k the bit at position 2k, so ctz>>1 is the offset.
// b must have at least 32 readable bytes.
TEXT ·neonDelimMask32(SB), NOSPLIT, $0-40
	MOVD	b_base+0(FP), R0
	MOVD	$0x40100401, R5
	VMOV	R5, V5.S4
	MOVD	$0x3B, R2
	VMOV	R2, V0.B16
	MOVD	$0x0A, R3
	VMOV	R3, V16.B16
	VLD1	(R0), [V1.B16, V2.B16]
	VCMEQ	V0.B16, V1.B16, V3.B16
	VCMEQ	V0.B16, V2.B16, V4.B16
	VAND	V5.B16, V3.B16, V3.B16
	VAND	V5.B16, V4.B16, V4.B16
	VADDP	V4.B16, V3.B16, V6.B16
	VADDP	V6.B16, V6.B16, V6.B16
	VMOV	V6.D[0], R9
	MOVD	R9, semi+24(FP)
	VCMEQ	V16.B16, V1.B16, V3.B16
	VCMEQ	V16.B16, V2.B16, V4.B16
	VAND	V5.B16, V3.B16, V3.B16
	VAND	V5.B16, V4.B16, V4.B16
	VADDP	V4.B16, V3.B16, V6.B16
	VADDP	V6.B16, V6.B16, V6.B16
	VMOV	V6.D[0], R10
	MOVD	R10, nl+32(FP)
	RET
