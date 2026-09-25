//go:build !darwin

package platform

import "context"

// DeviceIcon has no source outside macOS.
func DeviceIcon(context.Context, string) (string, bool) { return "", false }
