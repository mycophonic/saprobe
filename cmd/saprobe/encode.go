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
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/mycophonic/primordium/filesystem"
	flac "github.com/mycophonic/saprobe-flac"

	"github.com/mycophonic/saprobe"
)

func encodeCommand() *cli.Command {
	return &cli.Command{
		Name:      "encode",
		Usage:     "Encode raw PCM to FLAC",
		ArgsUsage: "<file>",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Value:   "-",
				Usage:   "output file path (- for stdout)",
			},
			&cli.IntFlag{
				Name:     "sample-rate",
				Aliases:  []string{"r"},
				Required: true,
				Usage:    "sample rate in Hz",
			},
			&cli.IntFlag{
				Name:     "bit-depth",
				Aliases:  []string{"b"},
				Required: true,
				Usage:    "bit depth (4, 8, 12, 16, 20, 24, 32)",
			},
			&cli.IntFlag{
				Name:     "channels",
				Aliases:  []string{"c"},
				Required: true,
				Usage:    "number of channels (1-8)",
			},
		},
		Action: runEncode,
	}
}

func runEncode(_ context.Context, cmd *cli.Command) error {
	if cmd.NArg() != 1 {
		return fmt.Errorf("%w: got %d", errInvalidArgCount, cmd.NArg())
	}

	path := cmd.Args().First()

	pcm, err := os.ReadFile(path) // #nosec G304 -- CLI tool reads user-specified audio files.
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	bitDepth, err := saprobe.ToBitDepth(
		uint8(cmd.Int("bit-depth")), // #nosec G115 -- CLI value validated by ToBitDepth.
	)
	if err != nil {
		return fmt.Errorf("invalid bit depth: %w", err)
	}

	format := flac.PCMFormat{
		SampleRate: cmd.Int("sample-rate"),
		BitDepth:   flac.BitDepth(bitDepth),
		Channels:   uint(cmd.Int("channels")),
	}

	output := cmd.String("output")
	if output == "-" {
		if err := flac.Encode(os.Stdout, pcm, format); err != nil {
			return fmt.Errorf("encoding: %w", err)
		}

		return nil
	}

	raw, err := os.Create(output) // #nosec G304 -- CLI tool creates user-specified output files.
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}

	file := filesystem.NewReadWriter(raw)
	defer file.Close()

	if err := flac.Encode(file, pcm, format); err != nil {
		return fmt.Errorf("encoding: %w", err)
	}

	return nil
}
