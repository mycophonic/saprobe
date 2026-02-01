package alac

import (
	"fmt"

	"github.com/mycophonic/saprobe"
)

// Element type tags from the ALAC bitstream.
const (
	elemSCE = 0 // Single Channel Element
	elemCPE = 1 // Channel Pair Element
	elemCCE = 2 // Coupling Channel Element (unsupported)
	elemLFE = 3 // LFE Channel Element
	elemDSE = 4 // Data Stream Element
	elemPCE = 5 // Program Config Element (unsupported)
	elemFIL = 6 // Fill Element
	elemEND = 7 // End of Frame
)

const (
	maxCoefs = 32

	// Special numActive value that triggers first-order delta decode mode.
	numActiveDelta = 31

	// Unused header field size in SCE/CPE (spec-defined).
	unusedHeaderBits = 12
)

// Decoder decodes ALAC audio packets into interleaved LE signed PCM.
type Decoder struct {
	config      Config
	format      saprobe.PCMFormat
	mixBufferU  []int32
	mixBufferV  []int32
	predictor   []int32
	shiftBuffer []uint16
	bits        bitBuffer // reusable bit reader (avoids per-packet allocation)
}

// NewDecoder creates a new ALAC decoder from the given configuration.
func NewDecoder(config Config) (*Decoder, error) {
	bitDepth, err := saprobe.ToBitDepth(config.BitDepth)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBitDepth, err)
	}

	frameLen := int(config.FrameLength)

	return &Decoder{
		config: config,
		format: saprobe.PCMFormat{
			SampleRate: int(config.SampleRate),
			BitDepth:   bitDepth,
			Channels:   uint(config.NumChannels),
		},
		mixBufferU:  make([]int32, frameLen),
		mixBufferV:  make([]int32, frameLen),
		predictor:   make([]int32, frameLen),
		shiftBuffer: make([]uint16, frameLen*2), // stereo worst case
	}, nil
}

// Format returns the PCM output format.
func (d *Decoder) Format() saprobe.PCMFormat {
	return d.format
}

// DecodePacket decodes a single ALAC packet into interleaved LE signed PCM bytes.
func (d *Decoder) DecodePacket(packet []byte) ([]byte, error) {
	numChan := int(d.config.NumChannels)
	bps := d.format.BitDepth.BytesPerSample()
	output := make([]byte, int(d.config.FrameLength)*numChan*bps)

	n, err := d.decodePacketInto(packet, output)
	if err != nil {
		return nil, err
	}

	return output[:n], nil
}

// decodePacketInto decodes a single ALAC packet into the provided output buffer.
// Returns the number of bytes written. The output buffer must be large enough
// to hold one full frame (FrameLength * NumChannels * BytesPerSample).
func (d *Decoder) decodePacketInto(packet, output []byte) (int, error) {
	d.bits.reset(packet)
	bits := &d.bits
	numSamples := d.config.FrameLength
	numChan := int(d.config.NumChannels)
	bps := d.format.BitDepth.BytesPerSample()
	chanIdx := 0

	for {
		if bits.pastEnd() {
			return 0, errBitstreamOverrun
		}

		tag := bits.readSmall(3)

		switch tag {
		case elemSCE, elemLFE:
			ns, err := d.decodeSCE(bits, output, chanIdx, numChan, numSamples)
			if err != nil {
				return 0, fmt.Errorf("alac: SCE/LFE decode: %w", err)
			}

			numSamples = ns
			chanIdx++

		case elemCPE:
			if chanIdx+2 > numChan {
				goto done
			}

			ns, err := d.decodeCPE(bits, output, chanIdx, numChan, numSamples)
			if err != nil {
				return 0, fmt.Errorf("alac: CPE decode: %w", err)
			}

			numSamples = ns
			chanIdx += 2

		case elemCCE, elemPCE:
			return 0, errUnsupportedElement

		case elemDSE:
			if err := d.skipDSE(bits); err != nil {
				return 0, err
			}

		case elemFIL:
			if err := d.skipFIL(bits); err != nil {
				return 0, err
			}

		case elemEND:
			bits.byteAlign()

			goto done

		default:
		}

		if chanIdx >= numChan {
			break
		}
	}

done:
	return int(numSamples) * numChan * bps, nil
}

// decodeSCE decodes a Single Channel Element (mono) or LFE element.
func (d *Decoder) decodeSCE(bits *bitBuffer, output []byte, chanIdx, numChan int, numSamples uint32) (uint32, error) {
	_ = bits.readSmall(4) // element instance tag

	// 12 unused header bits (must be 0).
	unusedHeader := bits.read(unusedHeaderBits)
	if unusedHeader != 0 {
		return 0, errInvalidHeader
	}

	headerByte := bits.read(4)
	partialFrame := headerByte >> 3
	bytesShifted := int((headerByte >> 1) & 0x3)

	if bytesShifted == 3 {
		return 0, errInvalidShift
	}

	escapeFlag := headerByte & 0x1
	chanBits := uint32(d.config.BitDepth) - uint32(bytesShifted)*8

	if partialFrame != 0 {
		numSamples = bits.read(16) << 16
		numSamples |= bits.read(16)
	}

	if escapeFlag == 0 {
		if err := d.decodeSCECompressed(bits, chanBits, bytesShifted, int(numSamples)); err != nil {
			return 0, err
		}
	} else {
		d.decodeSCEEscape(bits, chanBits, int(numSamples))

		bytesShifted = 0
	}

	// Write output.
	sampleCount := int(numSamples)

	switch d.config.BitDepth {
	case 16:
		writeMono16(output, d.mixBufferU, chanIdx, numChan, sampleCount)
	case 20:
		writeMono20(output, d.mixBufferU, chanIdx, numChan, sampleCount)
	case 24:
		writeMono24(output, d.mixBufferU, chanIdx, numChan, sampleCount, d.shiftBuffer, bytesShifted)
	case 32:
		writeMono32(output, d.mixBufferU, chanIdx, numChan, sampleCount, d.shiftBuffer, bytesShifted)

	default:
		panic(fmt.Sprintf("alac: decodeSCE called with unsupported bit depth %d", d.config.BitDepth))
	}

	return numSamples, nil
}

func (d *Decoder) decodeSCECompressed(bits *bitBuffer, chanBits uint32, bytesShifted, numSamples int) error {
	_ = bits.read(8) // mixBits (unused for mono)
	_ = bits.read(8) // mixRes (unused for mono)

	headerByte := bits.read(8)
	modeU := headerByte >> 4
	denShiftU := headerByte & 0xf

	headerByte = bits.read(8)
	pbFactorU := headerByte >> 5
	numU := headerByte & 0x1f

	var coefsU [maxCoefs]int16
	for i := range numU {
		coefsU[i] = int16(bits.read(16))
	}

	// Save shift bits position, skip past them.
	var shiftBits bitBuffer
	if bytesShifted != 0 {
		shiftBits = bits.copy()
		bits.advance(uint32(bytesShifted) * 8 * uint32(numSamples))
	}

	// Entropy decode.
	predBound := uint32(d.config.PB)

	var agP agParams
	setAGParams(&agP, uint32(d.config.MB), (predBound*pbFactorU)/4, uint32(d.config.KB),
		uint32(numSamples), uint32(numSamples), uint32(d.config.MaxRun))

	if err := dynDecomp(&agP, bits, d.predictor, numSamples, int(chanBits)); err != nil {
		return err
	}

	// Predictor.
	if modeU != 0 {
		unpcBlock(d.predictor, d.predictor, numSamples, nil, numActiveDelta, chanBits, 0)
	}

	unpcBlock(d.predictor, d.mixBufferU, numSamples, coefsU[:numU], int32(numU), chanBits, denShiftU)

	// Read shift buffer from saved position.
	if bytesShifted != 0 {
		shift := uint8(bytesShifted * 8)
		for i := range numSamples {
			d.shiftBuffer[i] = uint16(shiftBits.read(shift))
		}
	}

	return nil
}

func (d *Decoder) decodeSCEEscape(bits *bitBuffer, chanBits uint32, numSamples int) {
	shift := uint32(32) - chanBits

	if chanBits <= 16 {
		for idx := range numSamples {
			val := int32(bits.read(uint8(chanBits)))
			val = (val << shift) >> shift
			d.mixBufferU[idx] = val
		}
	} else {
		extraBits := chanBits - 16

		for idx := range numSamples {
			val := int32(bits.read(16))
			val = (val << 16) >> shift
			d.mixBufferU[idx] = val | int32(bits.read(uint8(extraBits)))
		}
	}
}

// decodeCPE decodes a Channel Pair Element (stereo).
func (d *Decoder) decodeCPE(bits *bitBuffer, output []byte, chanIdx, numChan int, numSamples uint32) (uint32, error) {
	_ = bits.readSmall(4) // element instance tag

	unusedHeader := bits.read(unusedHeaderBits)
	if unusedHeader != 0 {
		return 0, errInvalidHeader
	}

	headerByte := bits.read(4)
	partialFrame := headerByte >> 3
	bytesShifted := int((headerByte >> 1) & 0x3)

	if bytesShifted == 3 {
		return 0, errInvalidShift
	}

	escapeFlag := headerByte & 0x1
	// CPE has +1 bit for decorrelation.
	chanBits := uint32(d.config.BitDepth) - uint32(bytesShifted)*8 + 1

	if partialFrame != 0 {
		numSamples = bits.read(16) << 16
		numSamples |= bits.read(16)
	}

	var mixBits, mixRes int32

	if escapeFlag == 0 {
		var err error

		mixBits, mixRes, err = d.decodeCPECompressed(bits, chanBits, bytesShifted, int(numSamples))
		if err != nil {
			return 0, err
		}
	} else {
		chanBits = uint32(d.config.BitDepth) // Reset for escape.
		d.decodeCPEEscape(bits, chanBits, int(numSamples))

		bytesShifted = 0
	}

	// Unmix and write output.
	sampleCount := int(numSamples)

	switch d.config.BitDepth {
	case 16:
		writeStereo16(output, d.mixBufferU, d.mixBufferV, chanIdx, numChan, sampleCount, mixBits, mixRes)
	case 20:
		writeStereo20(output, d.mixBufferU, d.mixBufferV, chanIdx, numChan, sampleCount, mixBits, mixRes)
	case 24:
		writeStereo24(output, d.mixBufferU, d.mixBufferV, chanIdx, numChan, sampleCount,
			mixBits, mixRes, d.shiftBuffer, bytesShifted)
	case 32:
		writeStereo32(output, d.mixBufferU, d.mixBufferV, chanIdx, numChan, sampleCount,
			mixBits, mixRes, d.shiftBuffer, bytesShifted)

	default:
		panic(fmt.Sprintf("alac: decodeCPE called with unsupported bit depth %d", d.config.BitDepth))
	}

	return numSamples, nil
}

func (d *Decoder) decodeCPECompressed(
	bits *bitBuffer,
	chanBits uint32,
	bytesShifted, numSamples int,
) (int32, int32, error) { //revive:disable-line:confusing-results
	mixBits := int32(bits.read(8))
	mixRes := int32(int8(bits.read(8)))

	// Read U channel predictor params.
	headerByte := bits.read(8)
	modeU := headerByte >> 4
	denShiftU := headerByte & 0xf

	headerByte = bits.read(8)
	pbFactorU := headerByte >> 5
	numU := headerByte & 0x1f

	var coefsU [maxCoefs]int16
	for i := range numU {
		coefsU[i] = int16(bits.read(16))
	}

	// Read V channel predictor params.
	headerByte = bits.read(8)
	modeV := headerByte >> 4
	denShiftV := headerByte & 0xf

	headerByte = bits.read(8)
	pbFactorV := headerByte >> 5
	numV := headerByte & 0x1f

	var coefsV [maxCoefs]int16
	for i := range numV {
		coefsV[i] = int16(bits.read(16))
	}

	// Save shift bits position, skip past interleaved shift data.
	var shiftBits bitBuffer
	if bytesShifted != 0 {
		shiftBits = bits.copy()
		bits.advance(uint32(bytesShifted) * 8 * 2 * uint32(numSamples))
	}

	predBound := uint32(d.config.PB)

	var agP agParams

	// Decompress and predict U channel.
	setAGParams(&agP, uint32(d.config.MB), (predBound*pbFactorU)/4, uint32(d.config.KB),
		uint32(numSamples), uint32(numSamples), uint32(d.config.MaxRun))

	if err := dynDecomp(&agP, bits, d.predictor, numSamples, int(chanBits)); err != nil {
		return 0, 0, err
	}

	if modeU != 0 {
		unpcBlock(d.predictor, d.predictor, numSamples, nil, numActiveDelta, chanBits, 0)
	}

	unpcBlock(d.predictor, d.mixBufferU, numSamples, coefsU[:numU], int32(numU), chanBits, denShiftU)

	// Decompress and predict V channel.
	setAGParams(&agP, uint32(d.config.MB), (predBound*pbFactorV)/4, uint32(d.config.KB),
		uint32(numSamples), uint32(numSamples), uint32(d.config.MaxRun))

	if err := dynDecomp(&agP, bits, d.predictor, numSamples, int(chanBits)); err != nil {
		return 0, 0, err
	}

	if modeV != 0 {
		unpcBlock(d.predictor, d.predictor, numSamples, nil, numActiveDelta, chanBits, 0)
	}

	unpcBlock(d.predictor, d.mixBufferV, numSamples, coefsV[:numV], int32(numV), chanBits, denShiftV)

	// Read shift buffer from saved position.
	if bytesShifted != 0 {
		shift := uint8(bytesShifted * 8)
		for i := 0; i < numSamples*2; i += 2 {
			d.shiftBuffer[i+0] = uint16(shiftBits.read(shift))
			d.shiftBuffer[i+1] = uint16(shiftBits.read(shift))
		}
	}

	return mixBits, mixRes, nil
}

func (d *Decoder) decodeCPEEscape(bits *bitBuffer, chanBits uint32, numSamples int) {
	shift := uint32(32) - chanBits

	if chanBits <= 16 {
		for idx := range numSamples {
			val := int32(bits.read(uint8(chanBits)))
			val = (val << shift) >> shift
			d.mixBufferU[idx] = val

			val = int32(bits.read(uint8(chanBits)))
			val = (val << shift) >> shift
			d.mixBufferV[idx] = val
		}
	} else {
		extraBits := chanBits - 16

		for idx := range numSamples {
			val := int32(bits.read(16))
			val = (val << 16) >> shift
			d.mixBufferU[idx] = val | int32(bits.read(uint8(extraBits)))

			val = int32(bits.read(16))
			val = (val << 16) >> shift
			d.mixBufferV[idx] = val | int32(bits.read(uint8(extraBits)))
		}
	}
}

// skipFIL skips a Fill Element.
func (*Decoder) skipFIL(bits *bitBuffer) error {
	count := int16(bits.readSmall(4))
	if count == 15 { //revive:disable-line:add-constant
		count += int16(bits.readSmall(8)) - 1
	}

	bits.advance(uint32(count) * 8)

	if bits.pastEnd() {
		return errBitstreamOverrun
	}

	return nil
}

// skipDSE skips a Data Stream Element.
func (*Decoder) skipDSE(bits *bitBuffer) error {
	_ = bits.readSmall(4) // element instance tag
	dataByteAlignFlag := bits.readOne()

	count := uint16(bits.readSmall(8))
	if count == 255 { //revive:disable-line:add-constant
		count += uint16(bits.readSmall(8))
	}

	if dataByteAlignFlag != 0 {
		bits.byteAlign()
	}

	bits.advance(uint32(count) * 8)

	if bits.pastEnd() {
		return errBitstreamOverrun
	}

	return nil
}
