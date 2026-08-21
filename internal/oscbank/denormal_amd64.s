//go:build amd64

#include "textflag.h"

// MXCSR access for the denormal scope in denormal.go.
//
// STMXCSR and LDMXCSR only take memory operands, so both helpers bounce the
// word through a four-byte local slot rather than a register. The frame is
// eight bytes because Go keeps the stack pointer eight-byte aligned; only the
// low four are used.
//
// Neither helper touches anything the caller owns: AX is scratch under the Go
// ABI, no float registers are read or written, and there is no call, so both
// are NOSPLIT. LDMXCSR does serialize against in-flight SSE work, which is the
// reason this is entered once per block and not once per sample.

// func loadMXCSR() uint32
TEXT ·loadMXCSR(SB), NOSPLIT, $8-4
	STMXCSR mxcsr-8(SP)
	MOVL    mxcsr-8(SP), AX
	MOVL    AX, ret+0(FP)
	RET

// func storeMXCSR(mode uint32)
TEXT ·storeMXCSR(SB), NOSPLIT, $8-4
	MOVL    mode+0(FP), AX
	MOVL    AX, mxcsr-8(SP)
	LDMXCSR mxcsr-8(SP)
	RET
