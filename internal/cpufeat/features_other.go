//go:build !amd64 && !arm64

package cpufeat

func detect() Features {
	return Features{}
}
