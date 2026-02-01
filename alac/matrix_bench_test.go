//nolint:testpackage // Benchmarks unexported functions.
package alac

import "testing"

func BenchmarkWriteStereo16(b *testing.B) {
	const (
		numSamples = 4096
		numChan    = 2
	)

	out := make([]byte, numSamples*numChan*2)
	mixU := make([]int32, numSamples)
	mixV := make([]int32, numSamples)

	for i := range numSamples {
		mixU[i] = int32(i * 17)
		mixV[i] = int32(i * 13)
	}

	b.Run("mixRes=1", func(b *testing.B) {
		for range b.N {
			writeStereo16(out, mixU, mixV, 0, numChan, numSamples, 2, 1)
		}
	})

	b.Run("mixRes=0", func(b *testing.B) {
		for range b.N {
			writeStereo16(out, mixU, mixV, 0, numChan, numSamples, 0, 0)
		}
	})
}
