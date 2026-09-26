//go:build !darwin

package tune

import "context"

// RemoveOllamaAgent: there is no LaunchAgent outside macOS.
func RemoveOllamaAgent(context.Context) error { return nil }
