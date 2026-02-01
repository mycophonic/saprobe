module github.com/mycophonic/saprobe

go 1.25.6

require (
	github.com/abema/go-mp4 v1.4.1
	// Testing
	github.com/containerd/nerdctl/mod/tigron v0.0.0-20260121031139-a630881afd01
	github.com/jfreymuth/oggvorbis v1.0.5
	github.com/mewkiz/flac v1.0.13
	github.com/mycophonic/agar v0.1.2
	// Runtime
	github.com/mycophonic/primordium v0.0.0-20260131012359-6fb57c904cec
	// Third-party libraries
	github.com/urfave/cli/v3 v3.6.2
)

replace github.com/mewkiz/flac => github.com/mycophonic/flac v0.0.0-20260130024915-97b8621e14d3

require (
	github.com/creack/pty v1.1.24 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/icza/bitio v1.1.0 // indirect
	github.com/jfreymuth/vorbis v1.0.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirectik
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mewkiz/pkg v0.0.0-20250417130911-3f050ff8c56d // indirect
	github.com/mewpkg/term v0.0.0-20241026122259-37a80af23985 // indirect
	github.com/rs/zerolog v1.34.0 // indirect
	github.com/samber/lo v1.52.0 // indirect
	github.com/samber/slog-common v0.19.0 // indirect
	github.com/samber/slog-zerolog/v2 v2.9.0 // indirect
	golang.org/x/crypto v0.47.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/term v0.39.0 // indirect
	golang.org/x/text v0.33.0 // indirect
)
