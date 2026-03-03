//go:build linux && cgo && vst3go

package vst3

import (
	frameworkbus "github.com/justyntemme/vst3go/pkg/framework/bus"
	frameworkparam "github.com/justyntemme/vst3go/pkg/framework/param"
	frameworkprocess "github.com/justyntemme/vst3go/pkg/framework/process"
	frameworkplugin "github.com/justyntemme/vst3go/pkg/framework/plugin"
)

// Processor is the minimal vst3go processor used to validate plugin loading,
// parameter registration, and bus wiring before the synth engine is integrated.
type Processor struct {
	*frameworkplugin.BaseProcessor

	active bool
}

// NewProcessor creates a new minimal instrument processor.
func NewProcessor() *Processor {
	p := &Processor{
		BaseProcessor: frameworkplugin.NewBaseProcessor(frameworkbus.NewGenerator()),
	}
	registerParameters(p.Parameters())
	p.OnSetActive(func(active bool) error {
		p.active = active
		return nil
	})
	return p
}

// ProcessAudio clears the output buffers. This keeps the first spike real-time
// safe while the host integration layer is being validated.
func (p *Processor) ProcessAudio(ctx *frameworkprocess.Context) {
	ctx.Clear()
}

func registerParameters(registry *frameworkparam.Registry) {
	for _, spec := range ParameterSpecs() {
		builder := frameworkparam.New(uint32(spec.ID), spec.Name).
			ShortName(spec.Name).
			Range(spec.Min, spec.Max).
			Default(spec.Default).
			Unit(spec.Unit)
		if spec.Min == 0 && spec.Max == 1 && spec.Key == "chebyshev_enabled" {
			builder = builder.Toggle()
		}
		_ = registry.Add(builder.Build())
	}
}
