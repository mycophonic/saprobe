package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/mycophonic/saprobe"
	"github.com/mycophonic/saprobe/aac"
	"github.com/mycophonic/saprobe/alac"
	"github.com/mycophonic/saprobe/detect"
	"github.com/mycophonic/saprobe/flac"
	"github.com/mycophonic/saprobe/mp3"
	"github.com/mycophonic/saprobe/vorbis"
	"github.com/mycophonic/saprobe/wav"
)

var (
	errUnsupportedFormat = errors.New("unsupported audio format")
	errBitDepthMismatch  = errors.New("bit depth conversion is not yet implemented")
	errInvalidArgCount   = errors.New("expected exactly one argument: file path")
)

func decodeCommand() *cli.Command {
	return &cli.Command{
		Name:      "decode",
		Usage:     "Decode audio file to WAV (or raw PCM with --raw)",
		ArgsUsage: "<file>",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Value:   "-",
				Usage:   "output file path (- for stdout)",
			},
			&cli.IntFlag{
				Name:    "bit-depth",
				Aliases: []string{"b"},
				Value:   0,
				Usage:   "force output bit depth (16, 24, 32); 0 preserves native",
			},
			&cli.BoolFlag{
				Name:    "info",
				Aliases: []string{"i"},
				Usage:   "print format info and exit without decoding",
			},
			&cli.BoolFlag{
				Name:  "raw",
				Usage: "output raw PCM instead of WAV",
			},
		},
		Action: runDecode,
	}
}

func runDecode(_ context.Context, cmd *cli.Command) error {
	if cmd.NArg() != 1 {
		return fmt.Errorf("%w: got %d", errInvalidArgCount, cmd.NArg())
	}

	path := cmd.Args().First()

	file, err := os.Open(path) //nolint:gosec // CLI tool opens user-specified audio files
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer file.Close()

	codec, err := detect.Identify(file)
	if err != nil {
		return fmt.Errorf("detecting codec: %w", err)
	}

	switch codec {
	case detect.FLAC:
		return decodeAndOutput(cmd, "FLAC", file, flac.Decode)
	case detect.MP3:
		return decodeAndOutput(cmd, "MP3", file, mp3.Decode)
	case detect.Vorbis:
		return decodeAndOutput(cmd, "Vorbis", file, vorbis.Decode)
	case detect.ALAC:
		return decodeAndOutput(cmd, "ALAC", file, alac.Decode)
	case detect.WAV:
		return decodeAndOutput(cmd, "WAV", file, wav.Decode)
	case detect.AAC:
		return decodeAndOutput(cmd, "AAC", file, aac.Decode)
	case detect.Unknown:
		return fmt.Errorf("%s: %w", path, errUnsupportedFormat)
	}

	return fmt.Errorf("%s: %w", path, errUnsupportedFormat)
}

type decodeFunc func(io.ReadSeeker) ([]byte, saprobe.PCMFormat, error)

func decodeAndOutput(cmd *cli.Command, codecName string, rs io.ReadSeeker, decode decodeFunc) error {
	pcm, format, err := decode(rs)
	if err != nil {
		return fmt.Errorf("decoding %s: %w", codecName, err)
	}

	if cmd.Bool("info") {
		_, _ = fmt.Fprintf(os.Stderr, "codec:       %s\n", codecName)
		_, _ = fmt.Fprintf(os.Stderr, "sample rate: %d Hz\n", format.SampleRate)
		_, _ = fmt.Fprintf(os.Stderr, "bit depth:   %d\n", format.BitDepth)
		_, _ = fmt.Fprintf(os.Stderr, "channels:    %d\n", format.Channels)
		_, _ = fmt.Fprintf(os.Stderr, "pcm bytes:   %d\n", len(pcm))

		return nil
	}

	requestedDepth := cmd.Int("bit-depth")
	if requestedDepth > 0 && saprobe.BitDepth(requestedDepth) != format.BitDepth {
		return fmt.Errorf(
			"native is %d-bit, requested %d-bit: %w",
			format.BitDepth, requestedDepth, errBitDepthMismatch,
		)
	}

	if cmd.Bool("raw") {
		return writePCM(cmd.String("output"), pcm)
	}

	return writeWAV(cmd.String("output"), pcm, format)
}

func writePCM(output string, data []byte) error {
	if output == "-" {
		if _, err := os.Stdout.Write(data); err != nil {
			return fmt.Errorf("writing to stdout: %w", err)
		}

		return nil
	}

	file, err := os.Create(output) //nolint:gosec // CLI tool creates user-specified output files
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer file.Close()

	if _, err = file.Write(data); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

func writeWAV(output string, data []byte, format saprobe.PCMFormat) error {
	var w io.Writer

	if output == "-" {
		w = os.Stdout
	} else {
		file, err := os.Create(output) //nolint:gosec // CLI tool creates user-specified output files
		if err != nil {
			return fmt.Errorf("creating output file: %w", err)
		}

		defer file.Close()

		w = file
	}

	return wav.Encode(w, data, format)
}
