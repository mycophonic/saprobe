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
	"github.com/mycophonic/primordium/app/shutdown"
)

const name = "saprobe"

// version is the build's stamp: the shared build recipe sets it from `git describe`
// through -X main.version; a plain `go build` leaves "dev". gochecknoglobals
// exempts the name for this use.
var version = "dev"

func main() {
	appl := &cli.Command{
		Name:    name,
		Usage:   "Audio decoding cli (AIFF, ALAC, FLAC, DSD, AAC, MP3, OGG, WAV)",
		Version: version,
		Commands: []*cli.Command{
			decodeCommand(),
			encodeCommand(),
		},
	}

	// urfave/cli exits the process itself for its own errors; shutdown.Exit runs
	// the handlers first.
	cli.OsExiter = shutdown.Exit //nolint:reassign // cli.OsExiter is the package's documented hook for the exit

	// app.Main runs the command on a context a shutdown signal cancels, then the
	// handlers, and exits: 0 on nil, the asked status on an ExitError, which it
	// does not log; the error is printed here, once.
	app.Main(&app.Options{Name: name, Version: version}, func(ctx context.Context) error {
		if err := appl.Run(ctx, os.Args); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)

			return shutdown.ExitError(1)
		}

		return nil
	})
}
