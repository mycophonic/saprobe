package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/mycophonic/saprobe"
	"github.com/mycophonic/saprobe/flac"
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

	pcm, err := os.ReadFile(path) //nolint:gosec // CLI tool reads user-specified audio files.
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	bitDepth, err := saprobe.ToBitDepth(
		uint8(cmd.Int("bit-depth")), //nolint:gosec // CLI value validated by ToBitDepth.
	)
	if err != nil {
		return fmt.Errorf("invalid bit depth: %w", err)
	}

	format := saprobe.PCMFormat{
		SampleRate: cmd.Int("sample-rate"),
		BitDepth:   bitDepth,
		Channels:   uint(cmd.Int("channels")), //nolint:gosec // CLI value is 1-8.
	}

	output := cmd.String("output")
	if output == "-" {
		if err := flac.Encode(os.Stdout, pcm, format); err != nil {
			return fmt.Errorf("encoding: %w", err)
		}

		return nil
	}

	file, err := os.Create(output) //nolint:gosec // CLI tool creates user-specified output files.
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer file.Close()

	if err := flac.Encode(file, pcm, format); err != nil {
		return fmt.Errorf("encoding: %w", err)
	}

	return nil
}
