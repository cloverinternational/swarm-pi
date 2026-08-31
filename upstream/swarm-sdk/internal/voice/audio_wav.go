package voice

import (
	"encoding/binary"
	"fmt"
	"math"
)

const pcm16WAVHeaderSize = 44

// EncodePCM16LEToWAV wraps raw signed 16-bit little-endian PCM in a canonical
// RIFF/WAVE header. The PCM bytes are copied into the returned buffer unchanged.
func EncodePCM16LEToWAV(pcm []byte, sampleRate, channels int) ([]byte, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("sample rate must be positive, got %d", sampleRate)
	}
	if channels <= 0 || channels > math.MaxUint16 {
		return nil, fmt.Errorf("channels must be between 1 and %d, got %d", math.MaxUint16, channels)
	}

	blockAlign := channels * 2 // signed PCM16: two bytes per channel sample
	if len(pcm)%blockAlign != 0 {
		return nil, fmt.Errorf("PCM length %d is not a complete %d-byte channel frame", len(pcm), blockAlign)
	}
	if sampleRate > math.MaxUint32/blockAlign {
		return nil, fmt.Errorf("sample rate %d and %d channels overflow WAV byte rate", sampleRate, channels)
	}
	if uint64(len(pcm)) > uint64(math.MaxUint32)-(pcm16WAVHeaderSize-8) {
		return nil, fmt.Errorf("PCM data is too large for RIFF/WAVE: %d bytes", len(pcm))
	}

	wav := make([]byte, pcm16WAVHeaderSize+len(pcm))
	copy(wav[0:4], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(wav[20:22], 1)  // linear PCM
	binary.LittleEndian.PutUint16(wav[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(wav[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(wav[28:32], uint32(sampleRate*blockAlign))
	binary.LittleEndian.PutUint16(wav[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], uint32(len(pcm)))
	copy(wav[44:], pcm)

	return wav, nil
}
