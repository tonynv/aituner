package platform

import "testing"

func FuzzParsers(f *testing.F) {
	f.Add("Note: No thermal warning level has been recorded\nCPU_Speed_Limit = 50\n")
	f.Add("{ 1.23 1.50 1.70 }")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		_, _, _ = ParseTherm(s)
		_, _ = ParseLoadAvg(s)
		_ = Health{Cores: 10, Load1: 3, FreeMemPct: 10}.Warnings(s)
	})
}
