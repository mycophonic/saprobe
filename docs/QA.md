# QA

## Methodology

Uncompress with ffmpeg.

Uncompress with saprobe.

Compare bit for bit match. Pass or fail.

## Small run

### FLAC

```bash
go test -v -timeout 24h ./tests/ -run TestMassDecode -only flac -audio-path  /Volumes/Anisotope/gill 1>./_/masstest-flac.log  2>&1
```

```
    mass_decode_test.go:49: --- summary: 467 files ---
    mass_decode_test.go:49:   by codec: flac=467
    mass_decode_test.go:49:   by sample rate: 44100=467
    mass_decode_test.go:49:   by channels: 2=467
    mass_decode_test.go:49:   by bit depth: 16=467

PASS
ok      github.com/farcloser/saprobe/tests      707.436s
```

### ALAC/AAC

```bash
go test -v -timeout 24h ./tests/ -run TestMassDecode -only m4a -audio-path  /Volumes/Anisotope/gill 1>./_/masstest-mp4.log  2>&1
```


```
    mass_decode_test.go:51: --- summary: 7434 files ---
    mass_decode_test.go:51:   by codec: aac=105, alac=7329
    mass_decode_test.go:51:   by sample rate: 44100=7399, 48000=21, 96000=14
    mass_decode_test.go:51:   by channels: 1=2, 2=7432
    mass_decode_test.go:51:   by bit depth / bit rate: 16=7307, 24=22, N/A=105


FAIL
FAIL    github.com/mycophonic/saprobe/tests     12764.331s
FAIL
```

About 131 failures.

## OggVorbis

Success on... 14 files.

Do you folks have ogg files?

Please run: `go test -v -timeout 24h ./tests/ -run TestMassDecode -only m4a -audio-path MEDIA_DIRECTORY`.
