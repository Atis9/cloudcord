package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"

	"cloudcord/internal/tts"
)

func TestGeminiPCMOnlyReadsAudioData(t *testing.T) {
	wav := levelWAV(0.1, func(int) float64 { return 0.25 })
	binary.LittleEndian.PutUint32(wav[24:28], 24000)
	binary.LittleEndian.PutUint32(wav[28:32], 48000)
	withMetadata := append(bytes.Clone(wav), []byte("C2PA\x02\x00\x00\x00xx")...)
	binary.LittleEndian.PutUint32(withMetadata[4:8], uint32(len(withMetadata)-8))
	reader, pcm := geminiPCM(bytes.NewReader(withMetadata))
	if got, err := io.ReadAll(reader); !pcm || err != nil || !bytes.Equal(got, wav[44:]) {
		t.Fatalf("expected PCM without metadata, got %d bytes, %v", len(got), err)
	}
	reader, pcm = geminiPCM(bytes.NewReader(wav[:len(wav)-2]))
	if _, err := io.ReadAll(reader); !pcm || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected truncated PCM error, got %v", err)
	}
	binary.LittleEndian.PutUint32(wav[24:28], 48000)
	binary.LittleEndian.PutUint32(wav[28:32], 96000)
	reader, pcm = geminiPCM(bytes.NewReader(wav))
	if got, err := io.ReadAll(reader); pcm || err != nil || !bytes.Equal(got, wav) {
		t.Fatal("other WAV formats must reach FFmpeg intact")
	}
}

func TestFFmpegCancellationDuringWAVHeader(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	_, err := NewFFmpeg("").Convert(ctx, tts.AudioResult{MIME: "audio/wav", Stream: reader})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected header read to stop on cancellation, got %v", err)
	}
}
