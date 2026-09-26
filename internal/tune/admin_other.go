//go:build !darwin

package tune

import "context"

func runAdmin(context.Context, string) error { return ErrUnsupported }

// OllamaCLILink is the command-line link Ollama.app installs.
const OllamaCLILink = "/usr/local/bin/ollama"

// RemoveOllamaCLILink is macOS-only.
func RemoveOllamaCLILink(context.Context) error { return ErrUnsupported }
