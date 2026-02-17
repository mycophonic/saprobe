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

	"github.com/mycophonic/saprobe/version"
)

func main() {
	ctx := context.Background()
	app.New(ctx, version.Name())

	appl := &cli.Command{
		Name:    version.Name(),
		Usage:   "Audio decoding cli (AIFF, ALAC, FLAC, DSD, AAC, MP3, OGG, WAV)",
		Version: version.Version() + " (" + version.Commit() + " - " + version.Date() + ")",
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
