//go:build linux && cgo && vst3go

package main

import (
	"github.com/cwbudde/glockenspiel/plugin/vst3"
	vst3plugin "github.com/cwbudde/vst3go/pkg/plugin"

	_ "github.com/cwbudde/vst3go/pkg/plugin/cbridge"
)

func init() {
	vst3plugin.SetFactoryInfo(vst3plugin.FactoryInfo{
		Vendor: "cwbudde",
		URL:    "https://github.com/cwbudde/glockenspiel",
		Email:  "noreply@example.invalid",
	})

	vst3plugin.Register(vst3.Plugin{})
}

// Required for c-shared plugin builds.
func main() {}
