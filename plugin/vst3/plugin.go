//go:build linux && cgo && vst3go

package vst3

import (
	frameworkplugin "github.com/cwbudde/vst3go/pkg/framework/plugin"
	vst3plugin "github.com/cwbudde/vst3go/pkg/plugin"
)

const (
	pluginID       = "github.com.cwbudde.glockenspiel"
	pluginName     = "Glockenspiel"
	pluginVersion  = "0.1.0"
	pluginVendor   = "cwbudde"
	pluginCategory = "Instrument|Synth"
)

// Plugin is the first vst3go-backed plugin registration target for the project.
type Plugin struct{}

// GetInfo returns the stable plugin metadata exposed to the host.
func (Plugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       pluginID,
		Name:     pluginName,
		Version:  pluginVersion,
		Vendor:   pluginVendor,
		Category: pluginCategory,
	}
}

// CreateProcessor returns the minimal processor used for the first vst3go spike.
func (Plugin) CreateProcessor() vst3plugin.Processor {
	return NewProcessor()
}
