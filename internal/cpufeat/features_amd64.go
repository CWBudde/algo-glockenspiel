//go:build amd64

package cpufeat

import "golang.org/x/sys/cpu"

func detect() Features {
	return Features{
		HasAVX2: cpu.X86.HasAVX2,
	}
}
