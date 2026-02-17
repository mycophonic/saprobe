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

// Package pipeline describes audio signal chain transformations.
//
// A pipeline is a sequence of Steps, each recording the input format,
// output format, a human-readable description, and a fidelity Impact
// classification. Pipelines are built incrementally as audio flows
// through the system — the server adds decode steps, the endpoint
// appends hardware conversion steps — and the complete chain is
// presented to the user.
package pipeline

// Impact classifies how a pipeline step affects audio fidelity.
type Impact int

const (
	// BitPerfect means the transformation is lossless: bit packing,
	// lossless decompression, int↔float at the same depth.
	BitPerfect Impact = iota
	// Conversion means the transformation is technically lossy but
	// inaudible: DSD→PCM at nominal rate.
	Conversion
	// Destructive means the transformation is lossy: truncation,
	// sample rate conversion, bit depth reduction.
	Destructive
	// Unknown means the transformation's fidelity is outside our control:
	// driver-internal conversion in exclusive mode with a mixable format.
	Unknown
)

// Format describes an audio format at a pipeline boundary.
//
// This is a lightweight description for display purposes — it carries
// no hardware-specific fields (FrameBytes, IsPacked, etc.).
type Format struct {
	Type       string // "PCM" or "DSD".
	SampleRate int    // Sample rate in Hz.
	BitDepth   int    // Significant bits per sample.
	Channels   int
	IsInteger  bool // True = integer, false = float.
}

// Step describes one transformation in the audio signal chain.
type Step struct {
	Description string // Human-readable (e.g. "FLAC decode", "24→32 bit pack").
	Input       Format // Format entering this step.
	Output      Format // Format leaving this step.
	Impact      Impact
}
