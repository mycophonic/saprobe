// Package frame decodes MPEG Audio Layer 3 frames into raw PCM audio.
//
// It implements the complete MP3 decode pipeline: requantization, reorder,
// stereo processing, antialias, hybrid synthesis (IMDCT), frequency inversion,
// and polyphase subband synthesis.
//
// A single [Frame] is allocated once and reused across successive [Frame.Read]
// calls. Persistent state (bit reservoir, IMDCT overlap-add store, polyphase
// synthesis FIFO) is carried forward automatically. [Frame.Decode] writes
// interleaved 16-bit little-endian stereo PCM into a caller-provided buffer.
package frame
