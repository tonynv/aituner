//go:build !darwin && !linux

package platform

import "context"

type otherPlatform struct{}

func Current() Platform { return otherPlatform{} }

func (otherPlatform) Name() string { return "unsupported" }
func (otherPlatform) Detect(context.Context) (*Hardware, error) {
	return nil, ErrUnsupported
}
func (otherPlatform) DataDir() (string, error) { return "", ErrUnsupported }
