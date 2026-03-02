//go:build !amd64

package cpufeat

func detect() Features {
	return Features{}
}
