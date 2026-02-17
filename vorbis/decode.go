/*
Copyright Mycophonic.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package vorbis

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/jfreymuth/oggvorbis"

	"github.com/mycophonic/saprobe"
)

const floatBufSize = 4096 // samples per Read chunk from oggvorbis

// Decoder streams decoded PCM from an Ogg Vorbis source.
type Decoder struct {
	reader   *oggvorbis.Reader
	format   saprobe.PCMFormat
	floatBuf []float32

	// PCM byte buffer (after float->int16 conversion), drained by Read.
	buf    []byte
	bufOff int
	eof    bool
}

// NewDecoder opens an Ogg Vorbis stream and returns a streaming decoder.
func NewDecoder(rs io.ReadSeeker) (*Decoder, error) {
	reader, err := oggvorbis.NewReader(rs)
	if err != nil {
		return nil, fmt.Errorf("opening vorbis stream: %w", err)
	}

	return &Decoder{
		reader:   reader,
		floatBuf: make([]float32, floatBufSize),
		format: saprobe.PCMFormat{
			SampleRate: reader.SampleRate(),
			BitDepth:   saprobe.Depth16,
			Channels:   uint(reader.Channels()), //nolint:gosec // channel count is always small positive
		},
	}, nil
}

// Format returns the PCM output format.
func (d *Decoder) Format() saprobe.PCMFormat { return d.format }

// Read reads decoded PCM bytes from the Vorbis stream.
func (d *Decoder) Read(p []byte) (int, error) { //nolint:varnamelen // p is idiomatic for io.Reader.Read
	total := 0

	for len(p) > 0 {
		// Drain buffered PCM data.
		if d.bufOff < len(d.buf) {
			n := copy(p, d.buf[d.bufOff:])
			d.bufOff += n
			total += n
			p = p[n:]

			continue
		}

		if d.eof {
			if total > 0 {
				return total, nil
			}

			return 0, io.EOF
		}

		// Read next chunk of float32 samples.
		n, err := d.reader.Read(d.floatBuf) //nolint:varnamelen // n is idiomatic for read count
		if n == 0 && err != nil {
			d.eof = true

			if total > 0 {
				return total, nil
			}

			return 0, io.EOF
		}

		// Convert float32 -> clamped int16 LE.
		pcmBytes := n * 2

		if cap(d.buf) < pcmBytes {
			d.buf = make([]byte, pcmBytes)
		} else {
			d.buf = d.buf[:pcmBytes]
		}

		for i := range n {
			scaled := math.Round(float64(d.floatBuf[i]) * math.MaxInt16)
			scaled = max(math.MinInt16, min(math.MaxInt16, scaled))

			binary.LittleEndian.PutUint16(d.buf[i*2:], uint16(int16(scaled))) //nolint:gosec // clamped to int16 range
		}

		d.bufOff = 0
	}

	return total, nil
}
