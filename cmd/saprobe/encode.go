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

package main

import (
	"fmt"
	"os"

	"github.com/mycophonic/primordium/filesystem/iox"
	flac "github.com/mycophonic/saprobe-flac"

	"github.com/mycophonic/saprobe"
)

type encodeCmd struct {
	File string `arg:"" help:"raw PCM file to encode"`

	Output     string `default:"-"                                 help:"output file path (- for stdout)" short:"o"`
	SampleRate int    `help:"sample rate in Hz"                    required:"true"                        short:"r"`
	BitDepth   int    `help:"bit depth (4, 8, 12, 16, 20, 24, 32)" required:"true"                        short:"b"`
	Channels   int    `help:"number of channels (1-8)"             required:"true"                        short:"c"`
}

// Run encodes the file.
func (cmd *encodeCmd) Run() error {
	path := cmd.File

	pcm, err := os.ReadFile(path) // #nosec G304 -- CLI tool reads user-specified audio files.
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	bitDepth, err := saprobe.ToBitDepth(
		uint8(cmd.BitDepth), // #nosec G115 -- CLI value validated by ToBitDepth.
	)
	if err != nil {
		return fmt.Errorf("invalid bit depth: %w", err)
	}

	format := flac.PCMFormat{
		SampleRate: cmd.SampleRate,
		BitDepth:   flac.BitDepth(bitDepth),
		Channels:   uint(cmd.Channels),
	}

	output := cmd.Output
	if output == "-" {
		if err = flac.Encode(os.Stdout, pcm, format); err != nil {
			return fmt.Errorf("encoding: %w", err)
		}

		return nil
	}

	raw, err := os.Create(output) // #nosec G304 -- CLI tool creates user-specified output files.
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}

	file := iox.NewReadWriter(raw)

	// flac.Encode closes a writer that can be closed, and that close flushes
	// the file: its error is the encoder's. A failure before that point
	// leaves the file open, and the close then has nothing left to report.
	if err = flac.Encode(file, pcm, format); err != nil {
		_ = file.Close()

		return fmt.Errorf("encoding: %w", err)
	}

	return nil
}
