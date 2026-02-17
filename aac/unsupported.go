//go:build with_aac && !darwin

package aac

// CoreAudio AAC decoding requires macOS (darwin).
// Remove the with_aac build tag on this platform.
func init() {
	aacDecoderRequiresMacOS() // undefined: intentional compile error
}
