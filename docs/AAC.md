# AAC

We provide lip-service support for AAC: on macOS, you can provide a build
tag (with-aac), that will CGO into CoreAudio OS-level decoder.

Other platforms (linux and Windows) are unsupported.

Since this is provided by macOS, there is no point in testing / profiling,
or reporting support coverage: we support whatever CoreAudio does.

## Interesting notes

On really broken data, CoreAudio will exit clean with an error.
ffmpeg will survive, but generate garbage audio.

When there is no tag information about initial track delay, ffmpeg will return
the entire stream including leading garbage. CoreAudio instead assumes Apple encoder,
applying a default delay.