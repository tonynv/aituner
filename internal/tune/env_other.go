//go:build !darwin

package tune

import (
	"context"
)

type unsupportedRunner struct{}

func (unsupportedRunner) Apply(context.Context, Change) error  { return ErrUnsupported }
func (unsupportedRunner) Revert(context.Context, Change) error { return ErrUnsupported }

func NewRunner() Runner { return unsupportedRunner{} }

func ReadEnv(context.Context, int64, int64, bool) Env { return Env{} }
