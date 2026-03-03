//go:build linux && cgo && vst3go

package vst3

import "testing"

func TestNewProcessorRegistersAllParameters(t *testing.T) {
	processor := NewProcessor()

	got := processor.GetParameters().Count()
	want := int32(len(ParameterSpecs()))
	if got != want {
		t.Fatalf("unexpected parameter count: got %d want %d", got, want)
	}
}

func TestPluginInfo(t *testing.T) {
	info := (Plugin{}).GetInfo()
	if info.ID == "" {
		t.Fatal("expected non-empty plugin id")
	}
	if info.Name != pluginName {
		t.Fatalf("unexpected plugin name: got %q want %q", info.Name, pluginName)
	}
}
