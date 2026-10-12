/*
   Copyright Mycophonic.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/mycophonic/primordium/app"
)

const name = "saprobe"

// version is the build's stamp: the shared build recipe sets it from `git describe`
// through -X main.version; a plain `go build` leaves "dev". gochecknoglobals
// exempts the name for this use.
var version = "dev"

func main() {
	// app.New returns the context the shutdown handling cancels; running the
	// command on it is what lets a decode in flight see a signal.
	ctx := app.New(context.Background(), &app.Options{Name: name, Version: version})

	appl := &cli.Command{
		Name:    name,
		Usage:   "Audio decoding cli (AIFF, ALAC, FLAC, DSD, AAC, MP3, OGG, WAV)",
		Version: version,
		Commands: []*cli.Command{
			decodeCommand(),
			encodeCommand(),
		},
	}

	if err := appl.Run(ctx, os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)

		os.Exit(1)
	}
}
