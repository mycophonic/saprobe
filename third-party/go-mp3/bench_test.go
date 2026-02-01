// Copyright 2017 The go-mp3 Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mp3_test

import (
	"bytes"
	"io"
	"os"
	"testing"

	mp3 "github.com/mycophonic/saprobe/third-party/go-mp3"
)

func BenchmarkDecode(b *testing.B) {
	buf, err := os.ReadFile("example/classic.mp3")
	if err != nil {
		b.Fatal(err)
	}

	src := bytes.NewReader(buf)
	for range b.N {
		if _, err := src.Seek(0, io.SeekStart); err != nil {
			b.Fatal(err)
		}

		d, err := mp3.NewDecoder(src)
		if err != nil {
			b.Fatal(err)
		}

		if _, err := io.ReadAll(d); err != nil {
			b.Fatal(err)
		}
	}
}
