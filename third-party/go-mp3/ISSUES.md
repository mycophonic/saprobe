# go-mp3 Fork: Known Issues

Diagnosed from real-world MP3 decoding failures.

## 1. No free bitrate support

Free bitrate MP3 frames (BitrateIndex==0) are valid MP3 but not supported by
this decoder. `IsValid()` (`frameheader.go`) now rejects BitrateIndex==0, so the
sync scanner silently skips free bitrate frames instead of crashing. This means
legitimate free bitrate audio is silently dropped — no error, no decode.

**Triggered by**: 77 White.mp3 — malformed ID3 tag leaves 3840 bytes of orphaned
tag data between declared tag end and first audio frame. Scanner finds false sync
`0xffff0300` (MPEG1 Layer I, free bitrate) at position 37370. Real audio starts at 41209.

**Current state**: No longer crashes. Free bitrate frames silently skipped.

## 2. No Layer I/II support — silently skipped instead of decoded

This decoder only supports Layer III. `IsValid()` (`frameheader.go`) now rejects
Layer!=Layer3, so the sync scanner silently skips Layer I/II frames instead of
crashing. Reasonable for false syncs in trailing data (the original trigger), but
feeding an actual Layer I/II file will scan to EOF finding nothing — no useful
"unsupported format" error.

**Triggered by**: Part 3.mp3 — after 29018 valid Layer III frames, ~13.5KB of
trailing metadata (APE/LYRICS3 tags + ID3v1) follows. The sync scanner slides
755 bytes into this trailing data and finds false sync `0xfff6da33` (MPEG2 Layer I)
at position 30403784.

**Current state**: No longer crashes. Layer I/II sync candidates skipped by scanner.

## 3. No MPEG 2.5 support — silently skipped instead of decoded

`IsValid()` (`frameheader.go`) now rejects Version2.5, so the sync scanner
silently skips MPEG 2.5 frames instead of crashing. Same tradeoff as issue 2 —
no crash, but no useful error message for actual MPEG 2.5 files.

**Current state**: No longer crashes. MPEG 2.5 sync candidates skipped by scanner.

## 4. `skipTags` is naive

`source.go:skipTags()` only handles ONE tag at file start (either "ID3" or "TAG").
Problems:
- Doesn't handle malformed tag sizes (tag data extending beyond declared boundary)
- Doesn't handle multiple consecutive tags
- No awareness of trailing tags (ID3v1, APE, LYRICS3 at end of file)
- No handling of padding between tag and first audio frame

**Current state**: Unchanged.

## 5. No trailing metadata handling

`ensureFrameStartsAndLength()` (`decode.go`) scans frames to EOF with no
concept of "end of audio data." After the last valid frame, trailing metadata
(ID3v1 at filesize-128, APE tags, LYRICS3 tags) gets fed to the sync scanner.

The sync scanner is now more resilient to false syncs in trailing data (issues
1-3 no longer crash), but a false sync that passes all `IsValid()` checks could
still cause problems — attempting to parse side info or main data from garbage.

**Current state**: Less likely to crash due to IsValid() improvements. Structural
problem remains.

## 6. Sync recovery is limited

The sync scanner in `frameheader.Read` (`frameheader.go`) loops while
`!header.IsValid()`. Once a header passes `IsValid()`, the function returns it.
There is no retry mechanism if downstream parsing fails.

`IsValid()` now checks: sync word, version!=reserved, version!=2.5,
bitrateIndex!=0, bitrateIndex!=15, samplingFrequency!=reserved, layer!=reserved,
layer==Layer3, emphasis!=2. This covers the three gaps that previously caused
crashes (free bitrate, non-Layer3, MPEG 2.5).

Remaining weakness: a garbage header that passes all `IsValid()` checks will
still cause errors during side info or main data parsing, with no recovery path.

**Current state**: Better coverage in `IsValid()`. Still no retry on downstream
parse failure.
