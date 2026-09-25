//go:build !darwin

package platform

import "context"

// CheckHealth is only implemented on darwin; elsewhere it reports nothing rather than guessing.
func CheckHealth(context.Context) Health {
	return Health{FreeMemPct: -1, SpeedLimitPct: 100, GPUBusyPct: -1}
}
