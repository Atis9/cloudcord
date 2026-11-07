package audio

import (
	"bufio"
	"encoding/binary"
	"io"

	"cloudcord/internal/tts"
)

// Gemini and the Worker use a canonical 24 kHz mono PCM16 header. Passing
// those samples directly avoids FFmpeg's WAV startup probing. Other headers
// are replayed intact so FFmpeg still supports other valid WAV variants.
func geminiPCM(input io.Reader) (io.Reader, bool) {
	const headerSize = 44
	r := bufio.NewReader(input)
	h, err := r.Peek(headerSize)
	if err != nil || string(h[:4]) != "RIFF" || string(h[8:16]) != "WAVEfmt " ||
		binary.LittleEndian.Uint32(h[16:20]) != 16 || binary.LittleEndian.Uint16(h[20:22]) != 1 ||
		binary.LittleEndian.Uint16(h[22:24]) != 1 || binary.LittleEndian.Uint32(h[24:28]) != 24000 ||
		binary.LittleEndian.Uint32(h[28:32]) != 48000 || binary.LittleEndian.Uint16(h[32:34]) != 2 ||
		binary.LittleEndian.Uint16(h[34:36]) != 16 || string(h[36:40]) != "data" {
		return r, false
	}
	riffSize := binary.LittleEndian.Uint32(h[4:8])
	size := binary.LittleEndian.Uint32(h[40:44])
	if size == 0xffffffff && riffSize != 0xffffffff ||
		size != 0xffffffff && (size == 0 || size%2 != 0 || size > tts.MaxAudioBytes-headerSize || riffSize < size+36) {
		return r, false
	}
	_, _ = r.Discard(headerSize)
	if size == 0xffffffff {
		return r, true
	}
	// Finite WAVs can have metadata after their audio data.
	return &pcmData{io.LimitedReader{R: r, N: int64(size)}}, true
}

type pcmData struct{ io.LimitedReader }

func (r *pcmData) Read(p []byte) (int, error) {
	n, err := r.LimitedReader.Read(p)
	if err == io.EOF && r.N > 0 {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}
