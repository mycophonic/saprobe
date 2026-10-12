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
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/mycophonic/primordium/filesystem"
	alac "github.com/mycophonic/saprobe-alac"
	dsd "github.com/mycophonic/saprobe-dsd"
	flac "github.com/mycophonic/saprobe-flac"
	mp3 "github.com/mycophonic/saprobe-mp3"

	"github.com/mycophonic/saprobe"
	"github.com/mycophonic/saprobe/aac"
	"github.com/mycophonic/saprobe/aiff"
	"github.com/mycophonic/saprobe/detect"
	"github.com/mycophonic/saprobe/vorbis"
	"github.com/mycophonic/saprobe/wav"
)

const flagOutput = "output"

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
				Name:    flagOutput,
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
			&cli.IntFlag{
				Name:  "dsd-rate",
				Value: 0,
				Usage: "DSD output PCM sample rate (44100, 88200, 176400, 352800, 705600); 0 auto-selects",
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

	raw, err := os.Open(path) // #nosec G304 -- CLI tool opens user-specified audio files
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}

	file := filesystem.NewReadSeeker(raw)
	defer file.Close()

	codec, err := detect.Identify(file)
	if err != nil {
		return fmt.Errorf("detecting codec: %w", err)
	}

	switch codec {
	case detect.FLAC:
		return decodeAndOutput(cmd, "FLAC", file, newFlacDecoder)
	case detect.MP3:
		return decodeAndOutput(cmd, "MP3", file, newMP3Decoder)
	case detect.Vorbis:
		return decodeAndOutput(cmd, "Vorbis", file, newVorbisDecoder)
	case detect.ALAC:
		return decodeAndOutput(cmd, "ALAC", file, newALACDecoder)
	case detect.WAV:
		return decodeAndOutput(cmd, "WAV", file, newWAVDecoder)
	case detect.AAC:
		return decodeAndOutput(cmd, "AAC", file, newAACDecoder)
	case detect.AIFF:
		return decodeAndOutput(cmd, "AIFF", file, newAIFFDecoder)
	case detect.DSF, detect.DFF:
		outputRate, rateErr := dsdOutputRate(file, cmd.Int("dsd-rate"))
		if rateErr != nil {
			return rateErr
		}

		factory := func(rs io.ReadSeeker) (saprobe.PCMDecoder, error) {
			return newDSDDecoder(rs, outputRate)
		}

		return decodeAndOutput(cmd, "DSD", file, factory)
	case detect.Unknown:
		return fmt.Errorf("%s: %w", path, errUnsupportedFormat)
	}

	return fmt.Errorf("%s: %w", path, errUnsupportedFormat)
}

type decoderFactory func(io.ReadSeeker) (saprobe.PCMDecoder, error)

// Adapter functions to satisfy decoderFactory (concrete types → PCMDecoder).

// flacAdapter wraps flac.Decoder to satisfy saprobe.PCMDecoder,
// converting saprobe_flac.PCMFormat to saprobe.PCMFormat at the boundary.
type flacAdapter struct {
	*flac.Decoder
}

func (a *flacAdapter) Format() saprobe.PCMFormat {
	native := a.Decoder.Format()

	return saprobe.PCMFormat{
		SampleRate: native.SampleRate,
		BitDepth:   saprobe.BitDepth(native.BitDepth),
		Channels:   native.Channels,
	}
}

func newFlacDecoder(rs io.ReadSeeker) (saprobe.PCMDecoder, error) {
	dec, err := flac.NewDecoder(rs)
	if err != nil {
		return nil, fmt.Errorf("flac: %w", err)
	}

	return &flacAdapter{dec}, nil
}

// mp3Adapter wraps mp3.Decoder to satisfy saprobe.PCMDecoder,
// converting saprobe_mp3.PCMFormat to saprobe.PCMFormat at the boundary.
type mp3Adapter struct {
	*mp3.Decoder
}

func (a *mp3Adapter) Format() saprobe.PCMFormat {
	native := a.Decoder.Format()

	return saprobe.PCMFormat{
		SampleRate: native.SampleRate,
		BitDepth:   saprobe.BitDepth(native.BitDepth),
		Channels:   uint(native.Channels),
	}
}

func newMP3Decoder(rs io.ReadSeeker) (saprobe.PCMDecoder, error) {
	dec, err := mp3.NewDecoder(rs)
	if err != nil {
		return nil, fmt.Errorf("mp3: %w", err)
	}

	return &mp3Adapter{dec}, nil
}

func newVorbisDecoder(rs io.ReadSeeker) (saprobe.PCMDecoder, error) {
	dec, err := vorbis.NewDecoder(rs)
	if err != nil {
		return nil, fmt.Errorf("vorbis: %w", err)
	}

	return dec, nil
}

// alacAdapter wraps alac.Decoder to satisfy saprobe.PCMDecoder,
// converting alac.PCMFormat to saprobe.PCMFormat at the boundary.
type alacAdapter struct {
	*alac.Decoder
}

func (a *alacAdapter) Format() saprobe.PCMFormat {
	native := a.Decoder.Format()

	return saprobe.PCMFormat{
		SampleRate: native.SampleRate,
		BitDepth:   saprobe.BitDepth(native.BitDepth),
		Channels:   uint(native.Channels),
	}
}

func newALACDecoder(rs io.ReadSeeker) (saprobe.PCMDecoder, error) {
	dec, err := alac.NewDecoder(rs)
	if err != nil {
		return nil, fmt.Errorf("alac: %w", err)
	}

	return &alacAdapter{dec}, nil
}

func newWAVDecoder(rs io.ReadSeeker) (saprobe.PCMDecoder, error) {
	dec, err := wav.NewDecoder(rs)
	if err != nil {
		return nil, fmt.Errorf("wav: %w", err)
	}

	return dec, nil
}

func newAIFFDecoder(rs io.ReadSeeker) (saprobe.PCMDecoder, error) {
	dec, err := aiff.NewDecoder(rs)
	if err != nil {
		return nil, fmt.Errorf("aiff: %w", err)
	}

	return dec, nil
}

func newAACDecoder(rs io.ReadSeeker) (saprobe.PCMDecoder, error) {
	dec, err := aac.NewDecoder(rs)
	if err != nil {
		return nil, fmt.Errorf("aac: %w", err)
	}

	return dec, nil
}

// dsdAdapter wraps dsd.Decoder to satisfy saprobe.PCMDecoder,
// converting dsd.PCMFormat to saprobe.PCMFormat at the boundary.
type dsdAdapter struct {
	*dsd.Decoder
}

func (a *dsdAdapter) Format() saprobe.PCMFormat {
	native := a.Decoder.Format()

	return saprobe.PCMFormat{
		SampleRate: native.SampleRate,
		BitDepth:   saprobe.BitDepth(native.BitDepth),
		Channels:   uint(native.Channels),
		IsFloat:    true, // DSD decoder always outputs float32.
	}
}

func newDSDDecoder(rs io.ReadSeeker, outputRate dsd.OutputRate) (saprobe.PCMDecoder, error) {
	dec, err := dsd.NewDecoder(rs, outputRate)
	if err != nil {
		return nil, fmt.Errorf("dsd: %w", err)
	}

	return &dsdAdapter{dec}, nil
}

// dsdOutputRate resolves the DSD→PCM output rate from a CLI flag or auto-selection.
// When flagRate is 0, the DSD input rate is peeked from the container to choose
// a sensible default. NewDecoder re-parses the container from the start.
func dsdOutputRate(rs io.ReadSeeker, flagRate int) (dsd.OutputRate, error) {
	if flagRate > 0 {
		return dsd.OutputRate(flagRate), nil
	}

	raw, err := dsd.NewRawReader(rs)
	if err != nil {
		return 0, fmt.Errorf("dsd: %w", err)
	}

	return autoDSDRate(raw.DSDRate()), nil
}

// autoDSDRate selects a PCM output rate based on the DSD input rate.
// DSD64 → 176.4 kHz (16:1 ratio), everything else → 352.8 kHz.
func autoDSDRate(input dsd.InputRate) dsd.OutputRate {
	switch input {
	case dsd.DSD64:
		return dsd.Rate176400
	default:
		return dsd.Rate352800
	}
}

func decodeAndOutput(cmd *cli.Command, codecName string, rs io.ReadSeeker, factory decoderFactory) error {
	dec, err := factory(rs)
	if err != nil {
		return fmt.Errorf("decoding %s: %w", codecName, err)
	}

	if closer, ok := dec.(io.Closer); ok {
		defer closer.Close()
	}

	format := dec.Format()

	if cmd.Bool("info") {
		_, _ = fmt.Fprintf(os.Stderr, "codec:       %s\n", codecName)
		_, _ = fmt.Fprintf(os.Stderr, "sample rate: %d Hz\n", format.SampleRate)

		sampleType := "integer"
		if format.IsFloat {
			sampleType = "float"
		}

		_, _ = fmt.Fprintf(os.Stderr, "bit depth:   %d (%s)\n", format.BitDepth, sampleType)
		_, _ = fmt.Fprintf(os.Stderr, "channels:    %d\n", format.Channels)

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
		return streamPCM(cmd.String(flagOutput), dec)
	}

	// WAV output requires total size upfront — slurp all decoded PCM.
	pcm, err := io.ReadAll(dec)
	if err != nil {
		return fmt.Errorf("decoding %s: %w", codecName, err)
	}

	return writeWAV(cmd.String(flagOutput), pcm, format)
}

func streamPCM(output string, r io.Reader) error {
	var writer io.Writer

	if output == "-" {
		writer = os.Stdout
	} else {
		raw, err := os.Create(output) // #nosec G304 -- CLI tool creates user-specified output files
		if err != nil {
			return fmt.Errorf("creating output file: %w", err)
		}

		file := filesystem.NewReadWriter(raw)
		defer file.Close()

		writer = file
	}

	if _, err := io.Copy(writer, r); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

func writeWAV(output string, data []byte, format saprobe.PCMFormat) error {
	var writer io.Writer

	if output == "-" {
		writer = os.Stdout
	} else {
		raw, err := os.Create(output) // #nosec G304 -- CLI tool creates user-specified output files
		if err != nil {
			return fmt.Errorf("creating output file: %w", err)
		}

		file := filesystem.NewReadWriter(raw)
		defer file.Close()

		writer = file
	}

	if err := wav.Encode(writer, data, format); err != nil {
		return fmt.Errorf("encoding WAV: %w", err)
	}

	return nil
}
