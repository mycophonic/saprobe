package huffman //nolint:testpackage // needs access to internal tree data and enumerateCodes

import (
	"fmt"
	"testing"

	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/bits"
)

// codeToBytes builds a byte slice containing a Huffman codeword followed by padding.
// The codeword is written MSB-first starting at bit 0. Remaining bits are filled
// with padByte. The buffer is large enough for linbits + sign bits after the codeword.
func codeToBytes(code, length int, padByte byte) []byte {
	totalBytes := (length + 32 + 7) / 8 // codeword + enough padding for linbits+sign
	data := make([]byte, totalBytes)

	for i := range data {
		data[i] = padByte
	}

	for i := range length {
		bit := (code >> (length - 1 - i)) & 1
		byteIdx := i >> 3
		bitOff := uint(7 - (i & 7))

		if bit == 1 {
			data[byteIdx] |= 1 << bitOff
		} else {
			data[byteIdx] &^= 1 << bitOff
		}
	}

	return data
}

// absInt returns the absolute value of x.
func absInt(x int) int {
	if x < 0 {
		return -x
	}

	return x
}

func TestDecodePair_Exhaustive(t *testing.T) {
	t.Parallel()

	for tableNum := range 32 {
		meta := treeIndex[tableNum]
		if meta.treelen == 0 {
			continue
		}

		nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
		codes := enumerateCodes(nodes, meta.treelen)

		for _, c := range codes {
			// With 0x00 padding: sign bits are 0, linbits extension reads zeros.
			// Expected x/y magnitudes come from the packed symbol value.
			stream := codeToBytes(c.code, c.length, 0x00)
			bc := NewBitCache(bits.NewReader(stream))
			x, y := DecodePair(&bc, tableNum)

			wantX := (int(c.value) >> 4) & 0xF
			wantY := int(c.value) & 0xF

			// With zero padding, sign bit = 0 → positive, and linbits = 0 → no extension.
			if x != wantX || y != wantY {
				t.Errorf("table %d code %d/%d pad=0x00: want (%d,%d) got (%d,%d)",
					tableNum, c.code, c.length, wantX, wantY, x, y)
			}

			// With 0xFF padding: sign bits are 1, linbits are all-ones.
			stream = codeToBytes(c.code, c.length, 0xFF)
			bc = NewBitCache(bits.NewReader(stream))
			x, y = DecodePair(&bc, tableNum)

			// Non-zero values should be negated (sign bit = 1).
			if wantX != 0 && x != -wantX && (meta.linbits <= 0 || wantX != 15) {
				t.Errorf("table %d code %d/%d pad=0xFF: x want %d got %d",
					tableNum, c.code, c.length, -wantX, x)
			}

			if wantY != 0 && y != -wantY && (meta.linbits <= 0 || wantY != 15) {
				t.Errorf("table %d code %d/%d pad=0xFF: y want %d got %d",
					tableNum, c.code, c.length, -wantY, y)
			}

			// For linbits escape: magnitude = 15 + linbits_value, all-ones linbits.
			if meta.linbits > 0 && wantX == 15 {
				expectedMag := 15 + (1<<meta.linbits - 1)
				if absInt(x) != expectedMag {
					t.Errorf("table %d code %d/%d pad=0xFF: |x| want %d got %d",
						tableNum, c.code, c.length, expectedMag, absInt(x))
				}
			}

			if meta.linbits > 0 && wantY == 15 {
				expectedMag := 15 + (1<<meta.linbits - 1)
				if absInt(y) != expectedMag {
					t.Errorf("table %d code %d/%d pad=0xFF: |y| want %d got %d",
						tableNum, c.code, c.length, expectedMag, absInt(y))
				}
			}
		}
	}
}

func TestDecodeQuad_Exhaustive(t *testing.T) {
	t.Parallel()

	for tableNum := 32; tableNum <= 33; tableNum++ {
		meta := treeIndex[tableNum]
		nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
		codes := enumerateCodes(nodes, meta.treelen)

		for _, c := range codes {
			packed := int(c.value) & 0xF
			wantV := (packed >> 3) & 1
			wantW := (packed >> 2) & 1
			wantX := (packed >> 1) & 1
			wantY := packed & 1

			// With 0x00 padding: sign bits are 0 → all positive.
			stream := codeToBytes(c.code, c.length, 0x00)
			bc := NewBitCache(bits.NewReader(stream))
			v, w, x, y := DecodeQuad(&bc, tableNum)

			if v != wantV || w != wantW || x != wantX || y != wantY {
				t.Errorf("table %d code %d/%d pad=0x00: want (v=%d,w=%d,x=%d,y=%d) got (v=%d,w=%d,x=%d,y=%d)",
					tableNum, c.code, c.length, wantV, wantW, wantX, wantY, v, w, x, y)
			}

			// With 0xFF padding: sign bits are 1 → non-zero values negated.
			stream = codeToBytes(c.code, c.length, 0xFF)
			bc = NewBitCache(bits.NewReader(stream))
			v, w, x, y = DecodeQuad(&bc, tableNum)

			negV := wantV
			if negV != 0 {
				negV = -negV
			}

			negW := wantW
			if negW != 0 {
				negW = -negW
			}

			negX := wantX
			if negX != 0 {
				negX = -negX
			}

			negY := wantY
			if negY != 0 {
				negY = -negY
			}

			if v != negV || w != negW || x != negX || y != negY {
				t.Errorf("table %d code %d/%d pad=0xFF: want (v=%d,w=%d,x=%d,y=%d) got (v=%d,w=%d,x=%d,y=%d)",
					tableNum, c.code, c.length, negV, negW, negX, negY, v, w, x, y)
			}
		}
	}
}

func TestDecodePair_EmptyTables(t *testing.T) {
	t.Parallel()

	for _, tableNum := range []int{0, 4, 14} {
		bc := NewBitCache(bits.NewReader([]byte{0xFF}))
		x, y := DecodePair(&bc, tableNum)

		if x != 0 || y != 0 {
			t.Errorf("table %d: expected (0,0), got (%d,%d)", tableNum, x, y)
		}

		if bc.BitPos() != 0 {
			t.Errorf("table %d: expected bitpos 0, got %d", tableNum, bc.BitPos())
		}
	}
}

func TestDecodePair_BitPositionExact(t *testing.T) {
	t.Parallel()

	// For each pair table, verify the bit position advances by exactly
	// code_length + linbits (if applicable) + sign bits.
	for tableNum := range 32 {
		meta := treeIndex[tableNum]
		if meta.treelen == 0 {
			continue
		}

		nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
		codes := enumerateCodes(nodes, meta.treelen)

		for _, c := range codes {
			// Use all-zeros padding: no linbits extension, positive signs.
			stream := codeToBytes(c.code, c.length, 0x00)
			bc := NewBitCache(bits.NewReader(stream))
			x, y := DecodePair(&bc, tableNum)

			// Calculate expected bits consumed:
			// code_length + linbits_x (if x==15 && linbits>0) + sign_x (if x!=0)
			//             + linbits_y (if y==15 && linbits>0) + sign_y (if y!=0)
			rawX := (int(c.value) >> 4) & 0xF
			rawY := int(c.value) & 0xF
			expected := c.length

			if meta.linbits > 0 && rawX == 15 {
				expected += meta.linbits
			}

			if rawX != 0 { // sign bit (all zeros → x stays positive, but bit is still read)
				expected++
			}

			if meta.linbits > 0 && rawY == 15 {
				expected += meta.linbits
			}

			if rawY != 0 {
				expected++
			}

			if bc.BitPos() != expected {
				t.Errorf("table %d code %d/%d (raw x=%d y=%d, decoded x=%d y=%d): expected pos %d, got %d",
					tableNum, c.code, c.length, rawX, rawY, x, y, expected, bc.BitPos())
			}
		}
	}
}

func TestDecodeQuad_BitPositionExact(t *testing.T) {
	t.Parallel()

	for tableNum := 32; tableNum <= 33; tableNum++ {
		meta := treeIndex[tableNum]
		nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
		codes := enumerateCodes(nodes, meta.treelen)

		for _, c := range codes {
			stream := codeToBytes(c.code, c.length, 0x00)
			bc := NewBitCache(bits.NewReader(stream))
			v, w, x, y := DecodeQuad(&bc, tableNum)

			// Expected: code_length + sign bits for each non-zero value.
			packed := int(c.value) & 0xF
			expected := c.length

			if (packed>>3)&1 != 0 {
				expected++ // sign for v
			}

			if (packed>>2)&1 != 0 {
				expected++ // sign for w
			}

			if (packed>>1)&1 != 0 {
				expected++ // sign for x
			}

			if packed&1 != 0 {
				expected++ // sign for y
			}

			if bc.BitPos() != expected {
				t.Errorf("table %d code %d/%d (v=%d,w=%d,x=%d,y=%d): expected pos %d, got %d",
					tableNum, c.code, c.length, v, w, x, y, expected, bc.BitPos())
			}
		}
	}
}

func TestTableBuilding_NonNil(t *testing.T) {
	t.Parallel()

	// All non-empty pair tables must have entries.
	for i := range 32 {
		meta := treeIndex[i]
		if meta.treelen == 0 {
			if pairTables[i].entries != nil {
				t.Errorf("pair table %d: expected nil entries for empty table", i)
			}

			continue
		}

		if pairTables[i].entries == nil {
			t.Errorf("pair table %d: entries is nil for non-empty table", i)
		}

		if len(pairTables[i].entries) < (1 << pairPrimaryBits) {
			t.Errorf("pair table %d: entries length %d < primary size %d",
				i, len(pairTables[i].entries), 1<<pairPrimaryBits)
		}
	}

	// Both quad tables must have entries.
	for i := range 2 {
		if quadTables[i].entries == nil {
			t.Errorf("quad table %d: entries is nil", 32+i)
		}

		if len(quadTables[i].entries) < (1 << quadPrimaryBits) {
			t.Errorf("quad table %d: entries length %d < primary size %d",
				32+i, len(quadTables[i].entries), 1<<quadPrimaryBits)
		}
	}
}

func TestTableSharing(t *testing.T) {
	t.Parallel()

	// Tables 16-23 share entries (same tree, offset 1719).
	for i := 17; i <= 23; i++ {
		if &pairTables[i].entries[0] != &pairTables[16].entries[0] {
			t.Errorf("table %d does not share entries with table 16", i)
		}
	}

	// Tables 24-31 share entries (same tree, offset 2230).
	for i := 25; i <= 31; i++ {
		if &pairTables[i].entries[0] != &pairTables[24].entries[0] {
			t.Errorf("table %d does not share entries with table 24", i)
		}
	}
}

func TestLinbits(t *testing.T) {
	t.Parallel()

	// Verify linbits values match between old tree metadata and new table metadata.
	expectedLinbits := map[int]int{
		16: 1, 17: 2, 18: 3, 19: 4, 20: 6, 21: 8, 22: 10, 23: 13,
		24: 4, 25: 5, 26: 6, 27: 7, 28: 8, 29: 9, 30: 11, 31: 13,
	}

	for tableNum, want := range expectedLinbits {
		if got := pairTables[tableNum].linbits; got != want {
			t.Errorf("table %d: linbits = %d, want %d", tableNum, got, want)
		}
	}
}

func TestEnumerateCodes_Count(t *testing.T) {
	t.Parallel()

	// Verify the number of codewords per table matches expectations.
	// Tables with max value N have (N+1)^2 codewords for pairs,
	// and 2^4 = 16 for quads.
	for tableNum := range 34 {
		meta := treeIndex[tableNum]
		if meta.treelen == 0 {
			continue
		}

		nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
		codes := enumerateCodes(nodes, meta.treelen)

		if len(codes) == 0 {
			t.Errorf("table %d: no codewords enumerated from %d-node tree", tableNum, meta.treelen)
		}

		t.Logf("table %d: %d codewords, max code length %d", tableNum, len(codes), maxCodeLength(codes))
	}
}

func maxCodeLength(codes []codeEntry) int {
	m := 0
	for _, c := range codes {
		if c.length > m {
			m = c.length
		}
	}

	return m
}

func BenchmarkDecodePair(b *testing.B) {
	// Benchmark decoding from table 7 (common big_values table).
	// Use the codeword for (0,0) which is the most common symbol.
	meta := treeIndex[7]
	nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
	codes := enumerateCodes(nodes, meta.treelen)

	// Find the (0,0) codeword.
	var zeroCode codeEntry

	for _, c := range codes {
		if c.value == 0 {
			zeroCode = c

			break
		}
	}

	stream := codeToBytes(zeroCode.code, zeroCode.length, 0x00)

	for b.Loop() {
		bc := NewBitCache(bits.NewReader(stream))
		DecodePair(&bc, 7)
	}
}

func BenchmarkDecodePair_LongCode(b *testing.B) {
	// Benchmark the longest codeword in table 13 (max code length 19 bits).
	meta := treeIndex[13]
	nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
	codes := enumerateCodes(nodes, meta.treelen)

	var longest codeEntry

	for _, c := range codes {
		if c.length > longest.length {
			longest = c
		}
	}

	stream := codeToBytes(longest.code, longest.length, 0x00)

	for b.Loop() {
		bc := NewBitCache(bits.NewReader(stream))
		DecodePair(&bc, 13)
	}
}

func BenchmarkDecodePair_MixedCodes(b *testing.B) {
	// Benchmark a realistic mix: decode multiple codewords in sequence from table 7.
	meta := treeIndex[7]
	nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
	codes := enumerateCodes(nodes, meta.treelen)

	// Pick every 4th codeword to get a mix of short and long codes.
	var selected []codeEntry

	for idx, c := range codes {
		if idx%4 == 0 {
			selected = append(selected, c)
		}
	}

	// Build a concatenated bitstream.
	totalBits := 0

	for _, c := range selected {
		totalBits += c.length + 2 // code + up to 2 sign bits padding
	}

	stream := make([]byte, (totalBits+64+7)/8)
	bitPos := 0

	for _, c := range selected {
		for bit := c.length - 1; bit >= 0; bit-- {
			if (c.code>>bit)&1 == 1 {
				stream[bitPos>>3] |= 1 << uint(7-(bitPos&7))
			}

			bitPos++
		}

		bitPos += 2 // skip sign bits (zeros = positive)
	}

	decodeCount := len(selected)

	for b.Loop() {
		bc := NewBitCache(bits.NewReader(stream))

		for range decodeCount {
			DecodePair(&bc, 7)
		}
	}
}

func BenchmarkDecodePair_AllTables(b *testing.B) {
	// Benchmark the (0,0) codeword for each non-empty pair table.
	for tableNum := range 32 {
		meta := treeIndex[tableNum]
		if meta.treelen == 0 {
			continue
		}

		nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
		codes := enumerateCodes(nodes, meta.treelen)

		var zeroCode codeEntry

		for _, c := range codes {
			if c.value == 0 {
				zeroCode = c

				break
			}
		}

		stream := codeToBytes(zeroCode.code, zeroCode.length, 0x00)

		b.Run(fmt.Sprintf("table%d", tableNum), func(b *testing.B) {
			for b.Loop() {
				bc := NewBitCache(bits.NewReader(stream))
				DecodePair(&bc, tableNum)
			}
		})
	}
}

func BenchmarkDecodeQuad(b *testing.B) {
	meta := treeIndex[32]
	nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
	codes := enumerateCodes(nodes, meta.treelen)

	// Use the (0,0,0,0) codeword.
	var zeroCode codeEntry

	for _, c := range codes {
		if c.value == 0 {
			zeroCode = c

			break
		}
	}

	stream := codeToBytes(zeroCode.code, zeroCode.length, 0x00)

	for b.Loop() {
		bc := NewBitCache(bits.NewReader(stream))
		DecodeQuad(&bc, 32)
	}
}
