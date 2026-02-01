// Package huffman2 provides high-performance Huffman decoding for MPEG audio
// (ISO 11172-3 / 13818-3) spectral data.
//
// It replaces the previous bit-at-a-time tree walker with multi-level flat
// lookup tables built at init() from the canonical ISO Huffman tree data. Most
// codewords resolve in a single table lookup (O(1)). Only long codes — rare by
// Huffman construction — require secondary lookups.
//
// Entry point functions:
//
//   - [DecodePair] decodes a pair of spectral values from tables 0-31 (big_values region)
//   - [DecodeQuad] decodes four spectral values from tables 32-33 (count1 region)
//
// Both functions operate on a [BitCache], a 32-bit MSB-aligned bit window
// initialized from a [bits.Reader] via [NewBitCache]. The cache amortizes bit
// extraction cost across the decode loop (peek is a single shift, refill only
// when depleted). The caller updates the Reader's position after decoding.
package huffman
