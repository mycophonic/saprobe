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
		Usage:   "Audio decoding cli",
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
