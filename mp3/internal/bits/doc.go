// Package bits provides a zero-allocation, MSB-first bit reader over a byte slice.
//
// It is used for parsing MPEG audio bitstreams where fields are packed at arbitrary
// bit boundaries (e.g., 9-bit big_values, 4-bit scalefac_compress).
package bits
