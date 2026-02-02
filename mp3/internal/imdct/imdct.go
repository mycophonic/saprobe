package imdct

import (
	"math"
)

// IMDCT block size constants from the MP3 specification.
const (
	longN      = 36 // long block transform size
	longHalfN  = longN / 2
	shortN     = 12 // short block transform size
	shortHalfN = shortN / 2
)

// winData contains the four IMDCT window types (normal, start, short, stop).
var winData [4][longN]float32 //nolint:gochecknoglobals // precomputed lookup table

//nolint:gochecknoinits // precomputes lookup table
func init() {
	// Type 0: Normal window (long block).
	for i := range longN {
		winData[0][i] = float32(math.Sin(math.Pi / longN * (float64(i) + 0.5)))
	}

	// Type 1: Start window (long-to-short transition).
	for i := range longHalfN {
		winData[1][i] = float32(math.Sin(math.Pi / longN * (float64(i) + 0.5)))
	}

	for i := longHalfN; i < longHalfN+shortHalfN; i++ {
		winData[1][i] = 1.0
	}

	for i := longHalfN + shortHalfN; i < longHalfN+shortN; i++ {
		winData[1][i] = float32(math.Sin(math.Pi / shortN * (float64(i) + 0.5 - float64(longHalfN))))
	}

	for i := longHalfN + shortN; i < longN; i++ {
		winData[1][i] = 0.0
	}

	// Type 2: Short window (12-point).
	for i := range shortN {
		winData[2][i] = float32(math.Sin(math.Pi / shortN * (float64(i) + 0.5)))
	}

	for i := shortN; i < longN; i++ {
		winData[2][i] = 0.0
	}

	// Type 3: Stop window (short-to-long transition).
	for i := range shortHalfN {
		winData[3][i] = 0.0
	}

	for i := shortHalfN; i < shortN; i++ {
		winData[3][i] = float32(math.Sin(math.Pi / shortN * (float64(i) + 0.5 - float64(shortHalfN))))
	}

	for i := shortN; i < longHalfN; i++ {
		winData[3][i] = 1.0
	}

	for i := longHalfN; i < longN; i++ {
		winData[3][i] = float32(math.Sin(math.Pi / longN * (float64(i) + 0.5)))
	}
}

// cosN36 is the 36-point IMDCT cosine table, transposed so that the inner
// loop (over m, the input index) accesses sequential memory.
// Layout: cosN36[sampleIdx][m] = cos(pi/(2*36) * (2*sampleIdx+1+18) * (2*m+1)).
var cosN36 [longN][longHalfN]float32 //nolint:gochecknoglobals // precomputed lookup table

//nolint:gochecknoinits // precomputes lookup table
func init() {
	for sampleIdx := range longN {
		for m := range longHalfN {
			cosN36[sampleIdx][m] = float32(math.Cos(
				math.Pi / (2 * longN) * (2*float64(sampleIdx) + 1 + longHalfN) * (2*float64(m) + 1),
			))
		}
	}
}

// cosN12 is the 12-point IMDCT cosine table, transposed for cache-friendly
// inner loop access.
// Layout: cosN12[sampleIdx][m] = cos(pi/(2*12) * (2*sampleIdx+1+6) * (2*m+1)).
var cosN12 [shortN][shortHalfN]float32 //nolint:gochecknoglobals // precomputed lookup table

//nolint:gochecknoinits // precomputes lookup table
func init() {
	for sampleIdx := range shortN {
		for m := range shortHalfN {
			cosN12[sampleIdx][m] = float32(math.Cos(
				math.Pi / (2 * shortN) * (2*float64(sampleIdx) + 1 + shortHalfN) * (2*float64(m) + 1),
			))
		}
	}
}

// Win performs the IMDCT and windowing for one subband of MP3 audio data.
//
// input is the 18 frequency-domain coefficients for this subband.
// blockType selects the window shape (0=normal, 1=start, 2=short, 3=stop).
// out receives the 36 time-domain samples (overlap-add ready).
//
// For short blocks (blockType 2), three 12-point transforms are interleaved
// into the 36-sample output with 6-sample overlap between blocks.
func Win(input *[longHalfN]float32, blockType int, out *[longN]float32) {
	if blockType == 2 {
		*out = [longN]float32{}
		iwd := &winData[2]

		for blockIdx := range 3 {
			for sampleIdx := range shortN {
				cosRow := &cosN12[sampleIdx]

				sum := float32(0.0)
				for m := range shortHalfN {
					sum += input[blockIdx+3*m] * cosRow[m]
				}

				out[shortHalfN*blockIdx+sampleIdx+shortHalfN] += sum * iwd[sampleIdx]
			}
		}

		return
	}

	iwd := &winData[blockType]

	for sampleIdx := range longN {
		cosRow := &cosN36[sampleIdx]

		sum := float32(0.0)
		for m := range longHalfN {
			sum += input[m] * cosRow[m]
		}

		out[sampleIdx] = sum * iwd[sampleIdx]
	}
}
