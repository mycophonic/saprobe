// Package imdct implements the Inverse Modified Discrete Cosine Transform
// for MP3 audio decoding (ISO 11172-3 / ISO 13818-3).
//
// This is a zero-allocation replacement. All output
// buffers are caller-provided, cosine tables are transposed for sequential
// access in the inner loop, and parameters use fixed-size array pointers
// instead of slices.
package imdct
