package huffman

import "sort"

// Multi-level flat Huffman lookup tables, built at init() from canonical tree data.
//
// Entry format (int16):
//
//	Positive (leaf): bits[15:8] = bits to flush at this level, bits[7:0] = packed symbol
//	Negative (branch): bits[2:0] = next peek width, -(entry >> 3) = secondary table offset
//
// The decode loop peeks N bits, looks up the entry, and either extracts the symbol
// (leaf) or flushes bits and follows the branch to a secondary table. Most codewords
// resolve in a single primary lookup (O(1)). Only long codes (rare by Huffman
// construction) require secondary lookups.

const (
	pairPrimaryBits  = 5 // primary lookup width for pair tables (0-31)
	quadPrimaryBits  = 4 // primary lookup width for quad tables (32-33)
	maxSecondaryBits = 4 // maximum peek width per secondary level

	// Tree node encoding constants.
	leafMask     = 0xff00 // high byte; zero means leaf node
	lowByteMask  = 0xff   // low byte of tree node (right child offset)
	chainMinimum = 250    // offset threshold for chained navigation
	maxTableBase = 4095   // max secondary base offset encodable in int16 branch
)

// tableSet holds a flat lookup table and linbits for one Huffman table.
type tableSet struct {
	entries []int16 // flat lookup: primary entries followed by secondary entries
	linbits int     // linear extension bits for values == 15 (pair tables only)
}

//nolint:gochecknoglobals // built once at init from immutable ISO standard data
var (
	pairTables [32]tableSet // tables 0-31
	quadTables [2]tableSet  // tables 32-33
)

//nolint:gochecknoinits // tables must be ready before any decode call
func init() {
	buildPairTables()
	buildQuadTables()
}

func buildPairTables() {
	// Tables sharing the same tree offset share the entries slice.
	built := make(map[int][]int16) // tree offset → flat entries

	for tableNum := range 32 {
		meta := treeIndex[tableNum]
		if meta.treelen == 0 {
			continue // empty table (0, 4, 14)
		}

		entries, ok := built[meta.offset]
		if !ok {
			nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
			codes := enumerateCodes(nodes, meta.treelen)
			entries = buildFlatTable(codes, pairPrimaryBits)
			built[meta.offset] = entries
		}

		pairTables[tableNum] = tableSet{entries: entries, linbits: meta.linbits}
	}
}

func buildQuadTables() {
	for idx := range 2 {
		meta := treeIndex[32+idx]
		nodes := treeNodes[meta.offset : meta.offset+meta.treelen]
		codes := enumerateCodes(nodes, meta.treelen)
		quadTables[idx] = tableSet{entries: buildFlatTable(codes, quadPrimaryBits)}
	}
}

// codeEntry represents a single Huffman codeword extracted from the canonical tree.
type codeEntry struct {
	code   int   // bit pattern, right-aligned in length bits, MSB-first
	length int   // number of bits in the codeword
	value  uint8 // packed symbol: (x<<4)|y for pairs, (v<<3|w<<2|x<<1|y) for quads
}

// enumerateCodes walks a canonical Huffman tree and returns all codewords.
func enumerateCodes(nodes []uint16, treelen int) []codeEntry {
	entries := make([]codeEntry, 0, treelen/2+1)

	var walk func(idx, code, depth int)

	walk = func(idx, code, depth int) {
		if idx >= treelen {
			return
		}

		node := nodes[idx]
		if (node & leafMask) == 0 {
			// Leaf: high byte is 0, low byte is the packed symbol value.
			//nolint:gosec // node low byte is 0-255, fits uint8
			entries = append(entries, codeEntry{code, depth, uint8(node)})

			return
		}

		// Internal node: left child (bit 0), then right child (bit 1).
		walk(treeLeftChild(nodes, idx), code<<1, depth+1)
		walk(treeRightChild(nodes, idx), (code<<1)|1, depth+1)
	}

	walk(0, 0, 0)

	return entries
}

// treeLeftChild returns the index of the left child of the node at idx,
// following chained offsets for distant nodes (offset >= 250).
func treeLeftChild(nodes []uint16, idx int) int {
	point := idx

	for (nodes[point] >> 8) >= chainMinimum {
		point += int(nodes[point] >> 8)
	}

	point += int(nodes[point] >> 8)

	return point
}

// treeRightChild returns the index of the right child of the node at idx,
// following chained offsets for distant nodes (offset >= 250).
func treeRightChild(nodes []uint16, idx int) int {
	point := idx

	for (nodes[point] & lowByteMask) >= chainMinimum {
		point += int(nodes[point] & lowByteMask)
	}

	point += int(nodes[point] & lowByteMask)

	return point
}

// buildFlatTable builds a multi-level flat lookup table from enumerated codewords.
func buildFlatTable(codes []codeEntry, primaryBits int) []int16 {
	table := make([]int16, 0, 1<<primaryBits)
	buildTableLevel(&table, codes, primaryBits)

	return table
}

// buildTableLevel appends one lookup level to the table and returns the base offset
// of this level within the table slice.
func buildTableLevel(table *[]int16, codes []codeEntry, peekBits int) int {
	base := len(*table)
	size := 1 << peekBits
	*table = append(*table, make([]int16, size)...)

	// Partition codes into direct leaves and overflow groups keyed by prefix.
	overflow := make(map[int][]codeEntry)

	for _, entry := range codes {
		if entry.length <= peekBits {
			// Leaf: fill all entries that match this code (don't-care suffix bits).
			//nolint:gosec // length and value are small; no overflow risk
			leaf := int16(entry.length)<<8 | int16(entry.value)
			idx := entry.code << (peekBits - entry.length)
			count := 1 << (peekBits - entry.length)

			for j := range count {
				(*table)[base+idx+j] = leaf
			}
		} else {
			// Overflow: strip top peekBits, group by prefix for secondary table.
			prefix := entry.code >> (entry.length - peekBits)
			remaining := codeEntry{
				code:   entry.code & ((1 << (entry.length - peekBits)) - 1),
				length: entry.length - peekBits,
				value:  entry.value,
			}

			overflow[prefix] = append(overflow[prefix], remaining)
		}
	}

	// Build secondary tables for overflow groups in deterministic order.
	prefixes := make([]int, 0, len(overflow))
	for pfx := range overflow {
		prefixes = append(prefixes, pfx)
	}

	sort.Ints(prefixes)

	for _, prefix := range prefixes {
		subCodes := overflow[prefix]

		// Determine secondary peek width: min(maxRemaining, maxSecondaryBits).
		maxLen := 0
		for _, sc := range subCodes {
			if sc.length > maxLen {
				maxLen = sc.length
			}
		}

		nextPeek := min(maxLen, maxSecondaryBits)

		// Recursively build secondary table (appends to the same slice).
		secondaryBase := buildTableLevel(table, subCodes, nextPeek)

		if secondaryBase > maxTableBase {
			panic("huffman2: table offset exceeds int16 branch encoding capacity")
		}

		// Branch entry: negative int16.
		// Decode recovers: nextPeek = entry & 7, offset = -(entry >> 3).
		//nolint:gosec // secondaryBase checked above; fits in int16 encoding
		(*table)[base+prefix] = int16((-secondaryBase)<<3) | int16(nextPeek)
	}

	return base
}
