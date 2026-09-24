//go:build darwin

package platform

import (
	"os"
	"testing"
)

func FuzzApplySystemProfiler(f *testing.F) {
	if b, err := os.ReadFile("testdata/sp_m1max_studio.json"); err == nil {
		f.Add(b)
	}
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"SPHardwareDataType":[{"number_processors":"proc 999999999999999999999:1:1"}]}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		var h Hardware
		_ = applySystemProfiler(&h, b) // any input: error or fill, never panic
	})
}
