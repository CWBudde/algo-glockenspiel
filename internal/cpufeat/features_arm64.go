//go:build arm64

package cpufeat

// detect reports NEON unconditionally. Advanced SIMD is mandatory in ARMv8-A,
// so there is no hardware to interrogate: golang.org/x/sys/cpu derives the flag
// from the OS capability word, which is unreadable on some targets and comes
// back empty under emulation, and answering "no NEON" on an arm64 host would be
// wrong every time. A NEON kernel must not gate on this flag; it is here so a
// forced feature set can describe an arm64 host as completely as an amd64 one.
func detect() Features {
	return Features{HasASIMD: true}
}
