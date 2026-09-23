//go:build !darwin

package tune

import "context"

func runAdmin(context.Context, string) error { return ErrUnsupported }
