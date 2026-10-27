package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"testing"
	"time"

	"cloudcord/internal/tts"
)

func TestFFmpegNormalizesDecodedLevels(t *testing.T) {
	path := levelFFmpegPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var levels []float64
	for _, amplitude := range []float64{0.03, 0.6} {
		samples := convertAndDecode(t, ctx, path, levelWAV(2, func(i int) float64 {
			return amplitude * math.Sin(2*math.Pi*440*float64(i)/48000)
		}))
		rms, peak := sampleLevels(samples[9600 : len(samples)-9600])
		if rms < 0.05 || peak > 1 {
			t.Fatalf("unexpected output RMS=%g peak=%g", rms, peak)
		}
		levels = append(levels, 20*math.Log10(rms))
	}
	difference := math.Abs(levels[1] - levels[0])
	t.Logf("26 dB input difference -> %.2f dB decoded output difference (%v dBFS RMS)", difference, levels)
	if difference > 3 {
		t.Fatalf("volume difference remains %.2f dB", difference)
	}
}

func TestFFmpegLevelsSilenceShortSpeechAndTransients(t *testing.T) {
	path := levelFFmpegPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	silence := convertAndDecode(t, ctx, path, levelWAV(0.3, func(int) float64 { return 0 }))
	_, peak := sampleLevels(silence)
	// Lossy Opus decoding may add very low-level noise; keep it below -80 dBFS.
	if peak > 0.0001 {
		t.Fatalf("silence amplified into noise: %g", peak)
	}
	short := convertAndDecode(t, ctx, path, levelWAV(0.04, func(i int) float64 {
		return 0.03 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}))
	rms, _ := sampleLevels(short)
	if rms < 0.001 {
		t.Fatal("short utterance lost")
	}
	transient := convertAndDecode(t, ctx, path, levelWAV(2, func(i int) float64 {
		if i == 36000 {
			return 0.99
		}
		return 0.06 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}))
	_, peak = sampleLevels(transient)
	if peak >= 1 {
		t.Fatalf("decoded audio clips: %g", peak)
	}
}

func levelFFmpegPath(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("FFMPEG_PATH"); path != "" {
		return path
	}
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	return path
}

func levelWAV(seconds float64, sample func(int) float64) []byte {
	count := int(seconds * 48000)
	data := make([]byte, 44+count*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 48000)
	binary.LittleEndian.PutUint32(data[28:], 96000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(count*2))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(data[44+i*2:], uint16(int16(sample(i)*32767)))
	}
	return data
}

func convertAndDecode(t *testing.T, ctx context.Context, path string, wav []byte) []float32 {
	t.Helper()
	stream, err := NewFFmpeg(path).Convert(ctx, tts.AudioResult{MIME: "audio/wav", Stream: io.NopCloser(bytes.NewReader(wav))})
	if err != nil {
		t.Fatal(err)
	}
	frames, err := collect(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	ogg := page(0, 2, []byte{19}, head())
	tags := append([]byte("OpusTags"), make([]byte, 8)...)
	ogg = append(ogg, page(1, 0, []byte{byte(len(tags))}, tags)...)
	for i, frame := range frames {
		var lacing []byte
		for n := len(frame); n >= 255; n -= 255 {
			lacing = append(lacing, 255)
		}
		lacing = append(lacing, byte(len(frame)%255))
		flags := byte(0)
		if i == len(frames)-1 {
			flags = 4
		}
		data := page(uint32(i+2), flags, lacing, frame)
		binary.LittleEndian.PutUint64(data[6:], uint64((i+1)*960))
		binary.LittleEndian.PutUint32(data[22:], 0)
		binary.LittleEndian.PutUint32(data[22:], oggCRC(data))
		ogg = append(ogg, data...)
	}
	cmd := exec.CommandContext(ctx, path, "-hide_banner", "-loglevel", "error", "-f", "ogg", "-i", "pipe:0", "-ac", "1", "-ar", "48000", "-f", "f32le", "pipe:1")
	cmd.Stdin = bytes.NewReader(ogg)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 4 {
		t.Fatal("empty decoded audio")
	}
	result := make([]float32, len(raw)/4)
	for i := range result {
		result[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return result
}

func sampleLevels(samples []float32) (rms, peak float64) {
	for _, sample := range samples {
		value := float64(sample)
		rms += value * value
		peak = math.Max(peak, math.Abs(value))
	}
	return math.Sqrt(rms / float64(len(samples))), peak
}
