package bench

import (
	"context"
	"runtime"
	"sync"
	"time"
)

const (
	bwBufWords = 16 << 20 // 16 Mi uint64 = 128 MiB per buffer, far beyond L2/SLC
	bwReps     = 3
)

// MemBandwidth measures CPU-side streaming bandwidth with `threads` goroutines pinned to OS threads.
// copy traffic counts both the read and the write. This is CPU bandwidth; the GPU figure from MLX is the one
// that governs LLM token speed on Apple Silicon.
func MemBandwidth(ctx context.Context, threads, trials int) (copyGBs, readGBs []float64, err error) {
	if threads < 1 {
		threads = 1
	}
	src := make([][]uint64, threads)
	dst := make([][]uint64, threads)
	for i := range src {
		src[i] = make([]uint64, bwBufWords)
		dst[i] = make([]uint64, bwBufWords)
		for j := range src[i] { // touch every page and give the data non-zero content
			src[i][j] = uint64(j)*2654435761 + 1
		}
		for j := range dst[i] {
			dst[i][j] = 1
		}
	}
	run := func(work func(i int)) time.Duration {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < threads; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				<-start
				work(i)
			}(i)
		}
		t0 := time.Now()
		close(start)
		wg.Wait()
		return time.Since(t0)
	}
	sink := make([]uint64, threads)
	readWork := func(i int) {
		var a0, a1, a2, a3 uint64
		s := src[i]
		for r := 0; r < bwReps; r++ {
			for j := 0; j+4 <= len(s); j += 4 {
				a0 += s[j]
				a1 += s[j+1]
				a2 += s[j+2]
				a3 += s[j+3]
			}
		}
		sink[i] = a0 + a1 + a2 + a3
	}
	copyWork := func(i int) {
		for r := 0; r < bwReps; r++ {
			copy(dst[i], src[i])
		}
	}
	bytes := float64(bwBufWords) * 8 * bwReps * float64(threads)
	run(copyWork) // warmup
	run(readWork)
	for t := 0; t < trials; t++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		copyGBs = append(copyGBs, 2*bytes/run(copyWork).Seconds()/1e9)
		readGBs = append(readGBs, bytes/run(readWork).Seconds()/1e9)
	}
	_ = sink
	return copyGBs, readGBs, nil
}
