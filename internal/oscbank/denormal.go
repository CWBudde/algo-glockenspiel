package oscbank

import "runtime"

// DenormalScope is the floating-point mode FlushDenormals replaced, together
// with the OS-thread pin it took. Restore gives both back.
//
// Restore takes a pointer receiver so that it can mark the scope spent, which
// makes a second Restore a no-op rather than a second unlock of a pin the scope
// no longer holds. Keep the scope in a variable:
//
//	scope := oscbank.FlushDenormals()
//	defer scope.Restore()
//
// The one-line defer form does not compile against a pointer receiver, which is
// the point: a spent copy putting back a mode some later scope is relying on is
// exactly the bug this shape rules out.
type DenormalScope struct {
	saved uint64

	// pinned is whether this scope holds the OS-thread lock, changed whether it
	// also wrote the control register. They differ when the mode was already
	// flushing on entry: nothing to write, but the pin is still this scope's to
	// hold and to drop.
	pinned  bool
	changed bool
}

// FlushDenormals turns on flush-to-zero and denormals-are-zero for the calling
// goroutine and returns the scope that undoes it again:
//
//	scope := oscbank.FlushDenormals()
//	defer scope.Restore()
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
// pins the goroutine to its OS thread for its whole duration -- including when
// it finds the bits already set and writes nothing. The pin is what makes the
// guarantee above true at all: an unpinned goroutine can be moved onto a thread
// whose control register never had the bits, and the flushing would stop
// silently halfway through a block. It also keeps the restore honest, because a
// migrated goroutine would put the saved mode onto a thread we never touched
// while leaving our bits set on the thread we did, which is exactly the
// host-clobbering this API is meant to avoid. Restore must therefore run on the
// goroutine that called FlushDenormals, which a defer gives for free.
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
		// Already flushing, so there is nothing to write -- but the pin stays.
		// Dropping it here would leave the goroutine free to migrate onto a
		// thread that never had the bits, which is the failure this whole
		// mechanism exists to prevent.
		return DenormalScope{saved: saved, pinned: true}
	}

	setFPMode(saved | fpFlushMask)

	return DenormalScope{saved: saved, pinned: true, changed: true}
}

// Restore puts back the floating-point mode FlushDenormals replaced and drops
// the OS-thread pin it took. It must run on the goroutine that opened the
// scope. Restoring the zero value does nothing, and so does restoring a scope
// that has already been restored: a second call must not write a register or
// release a pin that by then belongs to some later scope.
func (s *DenormalScope) Restore() {
	if !s.pinned {
		return
	}

	if s.changed {
		setFPMode(s.saved)
	}

	s.pinned = false
	s.changed = false

	runtime.UnlockOSThread()
}

// FlushDenormalsSupported reports whether this architecture can flush
// denormals. It is false on every port without an assembly path, where
// FlushDenormals is a no-op.
func FlushDenormalsSupported() bool { return flushDenormalsSupported }
