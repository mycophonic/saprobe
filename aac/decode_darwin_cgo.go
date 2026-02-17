//go:build with_aac && darwin

package aac

/*
#cgo LDFLAGS: -framework AudioToolbox -framework CoreFoundation
#include <AudioToolbox/AudioToolbox.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	const char *data;
	int64_t     size;
} aac_reader;

static OSStatus aac_read_proc(
	void   *inClientData,
	SInt64  inPosition,
	UInt32  requestCount,
	void   *buffer,
	UInt32 *actualCount
) {
	aac_reader *r = (aac_reader *)inClientData;
	if (inPosition >= r->size) {
		*actualCount = 0;
		return noErr;
	}
	int64_t available = r->size - inPosition;
	UInt32 toRead = requestCount;
	if ((int64_t)toRead > available) {
		toRead = (UInt32)available;
	}
	memcpy(buffer, r->data + inPosition, toRead);
	*actualCount = toRead;
	return noErr;
}

static SInt64 aac_get_size_proc(void *inClientData) {
	aac_reader *r = (aac_reader *)inClientData;
	return (SInt64)r->size;
}

// decode_aac decodes AAC from an in-memory M4A buffer via AudioToolbox.
// On success (return 0), caller must free(*outBuf).
static int decode_aac(
	const char *data, int64_t dataSize,
	char **outBuf, int64_t *outBufSize,
	int *outSampleRate, int *outChannels
) {
	aac_reader reader;
	reader.data = data;
	reader.size = dataSize;

	AudioFileID audioFile = NULL;
	OSStatus status = AudioFileOpenWithCallbacks(
		&reader,
		aac_read_proc,
		NULL,
		aac_get_size_proc,
		NULL,
		kAudioFileM4AType,
		&audioFile
	);
	if (status != noErr) return (int)status;

	ExtAudioFileRef extFile = NULL;
	status = ExtAudioFileWrapAudioFileID(audioFile, false, &extFile);
	if (status != noErr) {
		AudioFileClose(audioFile);
		return (int)status;
	}

	// Query source format for sample rate and channel count.
	AudioStreamBasicDescription srcFormat;
	UInt32 propSize = sizeof(srcFormat);
	status = ExtAudioFileGetProperty(
		extFile, kExtAudioFileProperty_FileDataFormat, &propSize, &srcFormat
	);
	if (status != noErr) {
		ExtAudioFileDispose(extFile);
		AudioFileClose(audioFile);
		return (int)status;
	}

	*outSampleRate = (int)srcFormat.mSampleRate;
	*outChannels   = (int)srcFormat.mChannelsPerFrame;

	// Client format: 16-bit signed integer, little-endian (native on macOS), interleaved.
	AudioStreamBasicDescription clientFormat;
	memset(&clientFormat, 0, sizeof(clientFormat));
	clientFormat.mSampleRate       = srcFormat.mSampleRate;
	clientFormat.mFormatID         = kAudioFormatLinearPCM;
	clientFormat.mFormatFlags      = kAudioFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked;
	clientFormat.mBitsPerChannel   = 16;
	clientFormat.mChannelsPerFrame = srcFormat.mChannelsPerFrame;
	clientFormat.mBytesPerFrame    = 2 * srcFormat.mChannelsPerFrame;
	clientFormat.mFramesPerPacket  = 1;
	clientFormat.mBytesPerPacket   = clientFormat.mBytesPerFrame;

	status = ExtAudioFileSetProperty(
		extFile, kExtAudioFileProperty_ClientDataFormat, sizeof(clientFormat), &clientFormat
	);
	if (status != noErr) {
		ExtAudioFileDispose(extFile);
		AudioFileClose(audioFile);
		return (int)status;
	}

	// Total frame count for buffer allocation.
	SInt64 totalFrames = 0;
	propSize = sizeof(totalFrames);
	status = ExtAudioFileGetProperty(
		extFile, kExtAudioFileProperty_FileLengthFrames, &propSize, &totalFrames
	);
	if (status != noErr || totalFrames <= 0) {
		ExtAudioFileDispose(extFile);
		AudioFileClose(audioFile);
		return status != noErr ? (int)status : -1;
	}

	int64_t bufSize = totalFrames * clientFormat.mBytesPerFrame;
	char *buf = (char *)malloc(bufSize);
	if (!buf) {
		ExtAudioFileDispose(extFile);
		AudioFileClose(audioFile);
		return -1;
	}

	// Read all decoded PCM frames.
	int64_t framesRead = 0;
	while (framesRead < totalFrames) {
		UInt32 frameCount = (UInt32)(totalFrames - framesRead);

		AudioBufferList bufList;
		bufList.mNumberBuffers = 1;
		bufList.mBuffers[0].mNumberChannels = srcFormat.mChannelsPerFrame;
		bufList.mBuffers[0].mDataByteSize   = frameCount * clientFormat.mBytesPerFrame;
		bufList.mBuffers[0].mData           = buf + framesRead * clientFormat.mBytesPerFrame;

		status = ExtAudioFileRead(extFile, &frameCount, &bufList);
		if (status != noErr) {
			free(buf);
			ExtAudioFileDispose(extFile);
			AudioFileClose(audioFile);
			return (int)status;
		}
		if (frameCount == 0) break;
		framesRead += frameCount;
	}

	*outBuf     = buf;
	*outBufSize = framesRead * clientFormat.mBytesPerFrame;

	ExtAudioFileDispose(extFile);
	AudioFileClose(audioFile);
	return 0;
}
*/
import "C"

import (
	"fmt"
	"io"
	"unsafe"

	"github.com/mycophonic/saprobe"
)

// Decode reads an M4A/AAC stream and returns decoded 16-bit PCM via CoreAudio.
func Decode(rs io.ReadSeeker) ([]byte, saprobe.PCMFormat, error) {
	data, err := io.ReadAll(rs)
	if err != nil {
		return nil, saprobe.PCMFormat{}, fmt.Errorf("aac: reading input: %w", err)
	}

	if len(data) == 0 {
		return nil, saprobe.PCMFormat{}, fmt.Errorf("aac: empty input")
	}

	cData := C.CBytes(data)
	defer C.free(cData)

	var (
		outBuf     *C.char
		outSize    C.int64_t
		sampleRate C.int
		channels   C.int
	)

	result := C.decode_aac(
		(*C.char)(cData), C.int64_t(len(data)),
		&outBuf, &outSize,
		&sampleRate, &channels,
	)
	if result != 0 {
		return nil, saprobe.PCMFormat{}, fmt.Errorf("aac: CoreAudio error (OSStatus %d)", int(result))
	}

	defer C.free(unsafe.Pointer(outBuf))

	pcm := C.GoBytes(unsafe.Pointer(outBuf), C.int(outSize))

	format := saprobe.PCMFormat{
		SampleRate: int(sampleRate),
		BitDepth:   saprobe.Depth16,
		Channels:   uint(channels),
	}

	return pcm, format, nil
}
