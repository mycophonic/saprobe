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

package aac

import (
	"bytes"

	"github.com/mycophonic/saprobe"
)

// Decoder streams decoded PCM from an AAC source.
// On macOS with CGO, the entire file is decoded upfront via CoreAudio;
// Read then drains from the in-memory result. Without CGO support,
// NewDecoder returns ErrNotSupported.
type Decoder struct {
	reader *bytes.Reader
	format saprobe.PCMFormat
}

// Format returns the PCM output format.
func (d *Decoder) Format() saprobe.PCMFormat { return d.format }

// Read reads decoded PCM bytes from the AAC stream.
func (d *Decoder) Read(p []byte) (int, error) {
	//nolint:wrapcheck // bytes.Reader only returns io.EOF, no wrapping needed.
	return d.reader.Read(p)
}
