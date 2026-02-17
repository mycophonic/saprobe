//go:build !with_aac

package aac

import (
	"io"

	"github.com/mycophonic/saprobe"
)

// Decode returns ErrNotSupported when built without the with_aac tag.
func Decode(_ io.ReadSeeker) ([]byte, saprobe.PCMFormat, error) {
	return nil, saprobe.PCMFormat{}, ErrNotSupported
}
