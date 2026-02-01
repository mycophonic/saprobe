package testutils

import (
	"testing"

	"github.com/containerd/nerdctl/mod/tigron/test"

	"github.com/mycophonic/agar/pkg/agar"
)

// Setup creates a test case configured to run the saprobe binary.
func Setup() *test.Case {
	return agar.Setup("saprobe")
}

// AlacConvertPath returns the path to the alacconvert binary, or an empty
// string if it is not available. Build it with: make alacconvert.
func AlacConvertPath(t *testing.T) string {
	t.Helper()

	path, err := agar.LookFor("alacconvert")
	if err != nil {
		t.Log("alacconvert not found: run 'make alacconvert' to enable Apple ALAC reference tests")

		return ""
	}

	return path
}
