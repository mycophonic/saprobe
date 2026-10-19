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

	"github.com/alecthomas/kong"

	"github.com/mycophonic/primordium/app"
	"github.com/mycophonic/primordium/app/shutdown"
)

const name = "saprobe"

// version is the build's stamp: the shared build recipe sets it from `git describe`
// through -X main.version; a plain `go build` leaves "dev". gochecknoglobals
// exempts the name for this use.
var version = "dev"

type cli struct {
	Version kong.VersionFlag `help:"Print the version and exit." short:"v"`

	Decode decodeCmd `cmd:"" help:"Decode audio file to WAV (or raw PCM with --raw)"`
	Encode encodeCmd `cmd:"" help:"Encode raw PCM to FLAC"`
}

func main() {
	var flags cli

	// Parse and usage errors are kong's, printed with the usage before anything
	// runs; every failure exits 1, and --help and --version exit 0.
	parser := kong.Must(&flags,
		kong.Name(name),
		kong.Description("Audio decoding cli (AIFF, ALAC, FLAC, DSD, AAC, MP3, OGG, WAV)"),
		kong.Vars{"version": name + " version " + version},
		kong.UsageOnError(),
		kong.Exit(func(code int) {
			if code != 0 {
				code = 1
			}

			os.Exit(code)
		}),
	)

	// No command shows the help and succeeds, as the command did before kong,
	// which would otherwise report the missing command as an error.
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"--help"}
	}

	parsed, err := parser.Parse(args)
	parser.FatalIfErrorf(err)

	// app.Main runs the command on a context a shutdown signal cancels, then the
	// handlers, and exits: 0 on nil, the asked status on an ExitError, which it
	// does not log; the error is printed here, once.
	app.Main(&app.Options{Name: name, Version: version}, func(context.Context) error {
		if err := parsed.Run(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)

			return shutdown.ExitError(1)
		}

		return nil
	})
}
