//go:build linux

package platform

import "context"

type linuxPlatform struct{}

func Current() Platform { return linuxPlatform{} }

func (linuxPlatform) Name() string { return "linux" }
func (linuxPlatform) Detect(context.Context) (*Hardware, error) {
	return nil, ErrUnsupported
}
func (linuxPlatform) DataDir() (string, error) { return "", ErrUnsupported }
