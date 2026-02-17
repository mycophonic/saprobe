// Package aac provides AAC decoding via Apple CoreAudio (macOS only).
//
// This package requires the "with_aac" build tag and CGO_ENABLED=1 on macOS.
// Without the build tag, Decode returns ErrNotSupported.
// Using the build tag on non-macOS platforms is a compile error.
package aac
