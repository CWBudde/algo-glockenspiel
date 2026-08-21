package oscbank

import "runtime"

// DenormalScope is the floating-point mode FlushDenormals replaced. Restoring
// it puts the caller's mode back; the zero value restores nothing.
type DenormalScope struct {
	saved   uint64
	applied bool
}

// FlushDenormals turns on flush-to-zero and denormals-are-zero for the calling
// goroutine and returns the scope that undoes it again:
//
//	defer oscbank.FlushDenormals().Restore()
//
// Why this exists: a rotor that is no longer excited decays geometrically and
// spends a long stretch of its life in the denormal range, where the hardware
// traps into microcode and the recursion slows down by one to two orders of
// magnitude. With flush-to-zero the same rotor reaches exactly zero and stays
// cheap. The alternative -- testing every block against a magic floor, which is
// what the old model code did -- pays for the check on every block forever and
// still leaves the denormal multiplies in the kernel.
//
// What this guarantees, precisely:
//
//   - Between the call and Restore, on the OS thread that runs this goroutine,
//     denormal results flush to zero and denormal operands read as zero. That
//     covers both the assembly kernels and the portable Go one, because the
//     control bits live in the hardware's FP control register and not in any
//     instruction.
//   - The caller's mode is restored exactly. A VST3 host or a DAW may run the
//     audio thread with its own FTZ/DAZ policy, and it is not ours to overwrite
//     for the lifetime of the process. That is also why this is a scope rather
//     than an init().
//
// What it deliberately does not guarantee: nothing outside the scope, and
// nothing on other threads. FTZ/DAZ are per-hardware-thread state, so the scope
// pins the goroutine to its OS thread for its duration. That pin is not about
// speed -- if the goroutine migrated we would restore the saved mode onto a
// thread we never touched, while leaving our FTZ bits set on the thread we did,
// which is exactly the host-clobbering this API is meant to avoid. Restore must
// therefore run on the goroutine that called FlushDenormals, which a defer
// gives for free.
//
// The scope is entered per block rather than once per stream. A stream-wide
// scope would have to keep the render goroutine locked to its thread for the
// whole session, and that is a heavier promise to make to a host than the
// roughly forty cycles a save-set-restore costs against a block that takes
// thousands.
//
// On platforms with no reachable control register -- notably GOARCH=wasm, which
// this project builds for -- this is a documented no-op and denormals keep
// their IEEE behaviour.
func FlushDenormals() DenormalScope {
	if !flushDenormalsSupported {
		return DenormalScope{}
	}

	runtime.LockOSThread()

	saved := getFPMode()
	if saved&fpFlushMask == fpFlushMask {
		// Already flushing: leave the mode alone and drop the pin again, so a
		// host that set FTZ itself pays nothing here.
		runtime.UnlockOSThread()

		return DenormalScope{}
	}

	setFPMode(saved | fpFlushMask)

	return DenormalScope{saved: saved, applied: true}
}

// Restore puts back the floating-point mode FlushDenormals replaced. It must
// run on the goroutine that opened the scope. Restoring the zero value, or
// restoring twice, does nothing.
func (s DenormalScope) Restore() {
	if !s.applied {
		return
	}

	setFPMode(s.saved)
	runtime.UnlockOSThread()
}

// FlushDenormalsSupported reports whether this architecture can flush
// denormals. It is false on every port without an assembly path, where
// FlushDenormals is a no-op.
func FlushDenormalsSupported() bool { return flushDenormalsSupported }
