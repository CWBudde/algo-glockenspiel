//go:build amd64

package cpufeat

import "golang.org/x/sys/cpu"

func detect() Features {
	return Features{
		HasSSE2:     cpu.X86.HasSSE2,
		HasSSE3:     cpu.X86.HasSSE3,
		HasAVX:      cpu.X86.HasAVX,
		HasAVX2:     cpu.X86.HasAVX2,
		HasFMA:      cpu.X86.HasFMA,
		HasAVX512F:  cpu.X86.HasAVX512F,
		HasAVX512DQ: cpu.X86.HasAVX512DQ,
	}
}
