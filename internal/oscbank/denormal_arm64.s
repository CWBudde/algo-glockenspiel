//go:build arm64

#include "textflag.h"

// FPCR access for the denormal scope in denormal.go.
//
// FPCR is a system register, so it moves through a general-purpose register
// with MRS/MSR rather than being addressable as memory the way MXCSR is. R0 is
// scratch under the Go ABI and no float register is touched, so both helpers
// are NOSPLIT and leave the caller's state alone.
//
// MSR to FPCR is a context-synchronizing write on most implementations, which
// is the reason this is entered once per block and not once per sample.

// func loadFPCR() uint64
TEXT ·loadFPCR(SB), NOSPLIT, $0-8
	MRS  FPCR, R0
	MOVD R0, ret+0(FP)
	RET

// func storeFPCR(mode uint64)
TEXT ·storeFPCR(SB), NOSPLIT, $0-8
	MOVD mode+0(FP), R0
	MSR  R0, FPCR
	RET
