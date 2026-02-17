package aac

import "errors"

// ErrNotSupported is returned when AAC decoding is not available.
// Build with -tags=with_aac on macOS to enable CoreAudio AAC support.
var ErrNotSupported = errors.New("aac: not supported (build with -tags=with_aac on macOS)")
