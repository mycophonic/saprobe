package frameheader_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/mycophonic/saprobe/mp3/internal/consts"
	"github.com/mycophonic/saprobe/mp3/internal/frameheader"
)

// headerFields holds the fields needed to build a raw MPEG frame header.
type headerFields struct {
	version    uint32
	layer      uint32
	protection uint32
	bitrateIdx uint32
	sfreq      uint32
	padding    uint32
	mode       uint32
	modeExt    uint32
	emphasis   uint32
}

// build constructs a raw 32-bit MPEG frame header from individual fields.
func (f headerFields) build() uint32 {
	var h uint32 = 0xFFE00000 // sync word

	h |= (f.version & 0x3) << 19
	h |= (f.layer & 0x3) << 17
	h |= (f.protection & 0x1) << 16
	h |= (f.bitrateIdx & 0xF) << 12
	h |= (f.sfreq & 0x3) << 10
	h |= (f.padding & 0x1) << 9
	h |= (f.mode & 0x3) << 6
	h |= (f.modeExt & 0x3) << 4
	h |= f.emphasis & 0x3

	return h
}

// mpeg1Stereo128k44100 is a valid MPEG-1 Layer III 128kbps 44100Hz stereo header.
const mpeg1Stereo128k44100 = uint32(0xFFFB9000)

// mpeg2Mono32k22050Padded is a valid MPEG-2 Layer III 32kbps 22050Hz mono header with CRC and padding.
const mpeg2Mono32k22050Padded = uint32(0xFFF242C0)

// mpeg1JointStereo64k48000 is a valid MPEG-1 Layer III 64kbps 48000Hz joint stereo (MS+IS) header.
const mpeg1JointStereo64k48000 = uint32(0xFFFB5470)

func TestParse_ValidMPEG1Stereo(t *testing.T) {
	t.Parallel()

	h, err := frameheader.Parse(mpeg1Stereo128k44100)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}

	if got := h.Version(); got != consts.Version1 {
		t.Errorf("Version: got %d, want %d", got, consts.Version1)
	}

	if got := h.Layer(); got != consts.Layer3 {
		t.Errorf("Layer: got %d, want %d", got, consts.Layer3)
	}

	if h.Protected() {
		t.Error("Protected: got true, want false (protection_bit=1 means no CRC)")
	}

	if got := h.BitrateIndex(); got != 9 {
		t.Errorf("BitrateIndex: got %d, want 9", got)
	}

	if got := h.SamplingFrequency(); got != consts.SamplingFrequency44100 {
		t.Errorf("SamplingFrequency: got %d, want %d", got, consts.SamplingFrequency44100)
	}

	if h.Padded() {
		t.Error("Padded: got true, want false")
	}

	if got := h.Mode(); got != consts.ModeStereo {
		t.Errorf("Mode: got %d, want %d", got, consts.ModeStereo)
	}

	if got := h.Channels(); got != 2 {
		t.Errorf("Channels: got %d, want 2", got)
	}

	if got := h.Granules(); got != 2 {
		t.Errorf("Granules: got %d, want 2", got)
	}

	if got := h.LowSamplingFrequency(); got != 0 {
		t.Errorf("LowSamplingFrequency: got %d, want 0", got)
	}

	if got := h.SampleRateHz(); got != 44100 {
		t.Errorf("SampleRateHz: got %d, want 44100", got)
	}

	if got := h.Bitrate(); got != 128000 {
		t.Errorf("Bitrate: got %d, want 128000", got)
	}

	if got := h.FrameSize(); got != 417 {
		t.Errorf("FrameSize: got %d, want 417", got)
	}

	if got := h.SideInfoSize(); got != 32 {
		t.Errorf("SideInfoSize: got %d, want 32", got)
	}

	if got := h.BytesPerFrame(); got != 4608 {
		t.Errorf("BytesPerFrame: got %d, want 4608", got)
	}

	if h.UseMSStereo() {
		t.Error("UseMSStereo: got true, want false")
	}

	if h.UseIntensityStereo() {
		t.Error("UseIntensityStereo: got true, want false")
	}
}

func TestParse_ValidMPEG2Mono(t *testing.T) {
	t.Parallel()

	h, err := frameheader.Parse(mpeg2Mono32k22050Padded)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}

	if got := h.Version(); got != consts.Version2 {
		t.Errorf("Version: got %d, want %d", got, consts.Version2)
	}

	if !h.Protected() {
		t.Error("Protected: got false, want true (protection_bit=0 means CRC present)")
	}

	if got := h.Channels(); got != 1 {
		t.Errorf("Channels: got %d, want 1", got)
	}

	if got := h.Granules(); got != 1 {
		t.Errorf("Granules: got %d, want 1", got)
	}

	if got := h.LowSamplingFrequency(); got != 1 {
		t.Errorf("LowSamplingFrequency: got %d, want 1", got)
	}

	if got := h.SampleRateHz(); got != 22050 {
		t.Errorf("SampleRateHz: got %d, want 22050", got)
	}

	if got := h.Bitrate(); got != 32000 {
		t.Errorf("Bitrate: got %d, want 32000", got)
	}

	if got := h.FrameSize(); got != 105 {
		t.Errorf("FrameSize: got %d, want 105", got)
	}

	if got := h.SideInfoSize(); got != 9 {
		t.Errorf("SideInfoSize: got %d, want 9", got)
	}

	if got := h.BytesPerFrame(); got != 2304 {
		t.Errorf("BytesPerFrame: got %d, want 2304", got)
	}

	if !h.Padded() {
		t.Error("Padded: got false, want true")
	}
}

func TestParse_JointStereo(t *testing.T) {
	t.Parallel()

	h, err := frameheader.Parse(mpeg1JointStereo64k48000)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}

	if got := h.Mode(); got != consts.ModeJointStereo {
		t.Errorf("Mode: got %d, want %d", got, consts.ModeJointStereo)
	}

	if !h.UseMSStereo() {
		t.Error("UseMSStereo: got false, want true")
	}

	if !h.UseIntensityStereo() {
		t.Error("UseIntensityStereo: got false, want true")
	}

	if got := h.SampleRateHz(); got != 48000 {
		t.Errorf("SampleRateHz: got %d, want 48000", got)
	}

	if got := h.FrameSize(); got != 192 {
		t.Errorf("FrameSize: got %d, want 192", got)
	}
}

func TestParse_JointStereo_MSOnly(t *testing.T) {
	t.Parallel()

	// mode_ext = 2: MS stereo only, no intensity
	raw := headerFields{version: 3, layer: 1, protection: 1, bitrateIdx: 5, sfreq: 1, mode: 1, modeExt: 2}.build()

	h, err := frameheader.Parse(raw)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}

	if !h.UseMSStereo() {
		t.Error("UseMSStereo: got false, want true")
	}

	if h.UseIntensityStereo() {
		t.Error("UseIntensityStereo: got true, want false")
	}
}

func TestParse_JointStereo_IntensityOnly(t *testing.T) {
	t.Parallel()

	// mode_ext = 1: intensity stereo only, no MS
	raw := headerFields{version: 3, layer: 1, protection: 1, bitrateIdx: 5, sfreq: 1, mode: 1, modeExt: 1}.build()

	h, err := frameheader.Parse(raw)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}

	if h.UseMSStereo() {
		t.Error("UseMSStereo: got true, want false")
	}

	if !h.UseIntensityStereo() {
		t.Error("UseIntensityStereo: got false, want true")
	}
}

func TestParse_NonJointStereo_ModeExtIgnored(t *testing.T) {
	t.Parallel()

	// Stereo mode with mode_ext bits set — should NOT report MS/IS.
	raw := headerFields{version: 3, layer: 1, protection: 1, bitrateIdx: 5, sfreq: 1, mode: 0, modeExt: 3}.build()

	h, err := frameheader.Parse(raw)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}

	if h.UseMSStereo() {
		t.Error("UseMSStereo: got true, want false (not joint stereo)")
	}

	if h.UseIntensityStereo() {
		t.Error("UseIntensityStereo: got true, want false (not joint stereo)")
	}
}

func TestParse_AllSamplingFrequencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version uint32 // consts.Version value
		sfreq   uint32
		wantHz  int
		wantLSF int
	}{
		{"MPEG1 44100", 3, 0, 44100, 0},
		{"MPEG1 48000", 3, 1, 48000, 0},
		{"MPEG1 32000", 3, 2, 32000, 0},
		{"MPEG2 22050", 2, 0, 22050, 1},
		{"MPEG2 24000", 2, 1, 24000, 1},
		{"MPEG2 16000", 2, 2, 16000, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw := headerFields{version: tt.version, layer: 1, protection: 1, bitrateIdx: 5, sfreq: tt.sfreq}.build()

			h, err := frameheader.Parse(raw)
			if err != nil {
				t.Fatalf("Parse returned unexpected error: %v", err)
			}

			if got := h.SampleRateHz(); got != tt.wantHz {
				t.Errorf("SampleRateHz: got %d, want %d", got, tt.wantHz)
			}

			if got := h.LowSamplingFrequency(); got != tt.wantLSF {
				t.Errorf("LowSamplingFrequency: got %d, want %d", got, tt.wantLSF)
			}
		})
	}
}

func TestParse_SideInfoSizeTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		version  uint32
		mode     uint32
		wantSize int
	}{
		{"MPEG1 stereo", 3, 0, 32},
		{"MPEG1 mono", 3, 3, 17},
		{"MPEG2 stereo", 2, 0, 17},
		{"MPEG2 mono", 2, 3, 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw := headerFields{version: tt.version, layer: 1, protection: 1, bitrateIdx: 5, mode: tt.mode}.build()

			h, err := frameheader.Parse(raw)
			if err != nil {
				t.Fatalf("Parse returned unexpected error: %v", err)
			}

			if got := h.SideInfoSize(); got != tt.wantSize {
				t.Errorf("SideInfoSize: got %d, want %d", got, tt.wantSize)
			}
		})
	}
}

func TestParse_InvalidHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     uint32
		wantErr error
	}{
		{"no sync", 0x00000000, frameheader.ErrInvalidSync},
		{"partial sync", 0xFF000000, frameheader.ErrInvalidSync},
		{"reserved version", 0xFFEB9000, frameheader.ErrReservedVersion},
		{"MPEG 2.5", 0xFFE39000, frameheader.ErrUnsupportedMPEG2},
		{"reserved layer", 0xFFF99000, frameheader.ErrReservedLayer},
		{"Layer 2", 0xFFFD9000, frameheader.ErrUnsupportedLayer},
		{
			"Layer 1",
			headerFields{version: 3, layer: 3, protection: 1, bitrateIdx: 9}.build(),
			frameheader.ErrUnsupportedLayer,
		},
		{"bitrate 15", 0xFFFBF000, frameheader.ErrInvalidBitrate},
		{"free bitrate", 0xFFFB0000, frameheader.ErrFreeBitrate},
		{"reserved sfreq", 0xFFFB9C00, frameheader.ErrReservedSfreq},
		{"emphasis 2", 0xFFFB9002, frameheader.ErrInvalidEmphasis},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := frameheader.Parse(tt.raw)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Parse error: got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// fullReaderAdapter wraps an io.Reader to implement consts.FullReader.
type fullReaderAdapter struct {
	r io.Reader
}

func (f *fullReaderAdapter) ReadFull(buf []byte) (int, error) {
	return io.ReadFull(f.r, buf)
}

func TestRead_DirectSync(t *testing.T) {
	t.Parallel()

	var buf [4]byte

	binary.BigEndian.PutUint32(buf[:], mpeg1Stereo128k44100)

	source := &fullReaderAdapter{r: bytes.NewReader(buf[:])}

	h, pos, err := frameheader.Read(source, 0)
	if err != nil {
		t.Fatalf("Read returned unexpected error: %v", err)
	}

	if pos != 0 {
		t.Errorf("position: got %d, want 0", pos)
	}

	if got := h.Bitrate(); got != 128000 {
		t.Errorf("Bitrate: got %d, want 128000", got)
	}
}

func TestRead_SyncRecovery(t *testing.T) {
	t.Parallel()

	// 3 garbage bytes before a valid header.
	var headerBytes [4]byte

	binary.BigEndian.PutUint32(headerBytes[:], mpeg1Stereo128k44100)

	data := make([]byte, 0, 7)
	data = append(data, 0x00, 0xAA, 0x55)
	data = append(data, headerBytes[:]...)

	source := &fullReaderAdapter{r: bytes.NewReader(data)}

	h, pos, err := frameheader.Read(source, 0)
	if err != nil {
		t.Fatalf("Read returned unexpected error: %v", err)
	}

	if pos != 3 {
		t.Errorf("position: got %d, want 3", pos)
	}

	if got := h.Bitrate(); got != 128000 {
		t.Errorf("Bitrate: got %d, want 128000", got)
	}
}

func TestRead_PreservesStartPosition(t *testing.T) {
	t.Parallel()

	var headerBytes [4]byte

	binary.BigEndian.PutUint32(headerBytes[:], mpeg1Stereo128k44100)

	source := &fullReaderAdapter{r: bytes.NewReader(headerBytes[:])}

	_, pos, err := frameheader.Read(source, 42)
	if err != nil {
		t.Fatalf("Read returned unexpected error: %v", err)
	}

	if pos != 42 {
		t.Errorf("position: got %d, want 42", pos)
	}
}

func TestRead_CleanEOF(t *testing.T) {
	t.Parallel()

	source := &fullReaderAdapter{r: bytes.NewReader(nil)}

	_, _, err := frameheader.Read(source, 0)
	if !errors.Is(err, io.EOF) {
		t.Errorf("Read error: got %v, want io.EOF", err)
	}
}

func TestRead_UnexpectedEOF_ShortInitialRead(t *testing.T) {
	t.Parallel()

	// Only 2 bytes — not enough for a header.
	source := &fullReaderAdapter{r: bytes.NewReader([]byte{0xFF, 0xFB})}

	_, _, err := frameheader.Read(source, 0)
	if err == nil {
		t.Fatal("Read should have returned an error")
	}

	var eof *consts.UnexpectedEOF
	if !errors.As(err, &eof) {
		t.Errorf("Read error: got %T (%v), want *consts.UnexpectedEOF", err, err)
	}
}

func TestRead_UnexpectedEOF_DuringSyncScan(t *testing.T) {
	t.Parallel()

	// 4 bytes that form an invalid header, then EOF — sync scan hits EOF.
	source := &fullReaderAdapter{r: bytes.NewReader([]byte{0xFF, 0xE0, 0x00, 0x00})}

	_, _, err := frameheader.Read(source, 0)
	if err == nil {
		t.Fatal("Read should have returned an error")
	}

	var eof *consts.UnexpectedEOF
	if !errors.As(err, &eof) {
		t.Errorf("Read error: got %T (%v), want *consts.UnexpectedEOF", err, err)
	}
}

func TestParse_AllBitrateIndices_MPEG1(t *testing.T) {
	t.Parallel()

	wantBitrates := []int{
		0, 32000, 40000, 48000, 56000, 64000, 80000, 96000,
		112000, 128000, 160000, 192000, 224000, 256000, 320000,
	}

	for idx := 1; idx <= 14; idx++ {
		raw := headerFields{version: 3, layer: 1, protection: 1, bitrateIdx: uint32(idx)}.build()

		h, err := frameheader.Parse(raw)
		if err != nil {
			t.Fatalf("bitrateIndex=%d: Parse returned unexpected error: %v", idx, err)
		}

		if got := h.Bitrate(); got != wantBitrates[idx] {
			t.Errorf("bitrateIndex=%d: Bitrate: got %d, want %d", idx, got, wantBitrates[idx])
		}
	}
}

func TestParse_AllBitrateIndices_MPEG2(t *testing.T) {
	t.Parallel()

	wantBitrates := []int{
		0, 8000, 16000, 24000, 32000, 40000, 48000, 56000,
		64000, 80000, 96000, 112000, 128000, 144000, 160000,
	}

	for idx := 1; idx <= 14; idx++ {
		raw := headerFields{version: 2, layer: 1, protection: 1, bitrateIdx: uint32(idx)}.build()

		h, err := frameheader.Parse(raw)
		if err != nil {
			t.Fatalf("bitrateIndex=%d: Parse returned unexpected error: %v", idx, err)
		}

		if got := h.Bitrate(); got != wantBitrates[idx] {
			t.Errorf("bitrateIndex=%d: Bitrate: got %d, want %d", idx, got, wantBitrates[idx])
		}
	}
}

func TestParse_Emphasis(t *testing.T) {
	t.Parallel()

	// Emphasis 0, 1, and 3 are valid; 2 is invalid.
	validEmphasis := []uint32{0, 1, 3}
	for _, e := range validEmphasis {
		raw := headerFields{version: 3, layer: 1, protection: 1, bitrateIdx: 9, emphasis: e}.build()
		if _, err := frameheader.Parse(raw); err != nil {
			t.Errorf("emphasis=%d: unexpected error: %v", e, err)
		}
	}

	// Emphasis 2 is invalid.
	raw := headerFields{version: 3, layer: 1, protection: 1, bitrateIdx: 9, emphasis: 2}.build()
	if _, err := frameheader.Parse(raw); !errors.Is(err, frameheader.ErrInvalidEmphasis) {
		t.Errorf("emphasis=2: got %v, want ErrInvalidEmphasis", err)
	}
}
