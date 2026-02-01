// Copyright 2017 Hajime Hoshi
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

package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/hajimehoshi/oto/v2"

	"github.com/mycophonic/saprobe/third-party/go-mp3"
)

func run() error {
	file, err := os.Open("classic.mp3")
	if err != nil {
		return err
	}
	defer file.Close()

	decoder, err := mp3.NewDecoder(file)
	if err != nil {
		return err
	}

	context, ready, err := oto.NewContext(decoder.SampleRate(), 2, 2)
	if err != nil {
		return err
	}

	<-ready

	player := context.NewPlayer(decoder)
	defer player.Close()

	player.Play()

	fmt.Printf("Length: %d[bytes]\n", decoder.Length()) //nolint:forbidigo // example program

	for {
		time.Sleep(time.Second)

		if !player.IsPlaying() {
			break
		}
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
