//go:build race

package bench

// The race detector instruments every memory access, so throughput assertions are meaningless under it.
const raceEnabled = true
