package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"cloudcord/internal/tts"
)

func page(seq uint32, flags byte, lacing, body []byte) []byte {
	h := make([]byte, 27)
	copy(h, "OggS")
	h[5] = flags
	binary.LittleEndian.PutUint32(h[14:], 1)
	binary.LittleEndian.PutUint32(h[18:], seq)
	h[26] = byte(len(lacing))
	data := append(append(h, lacing...), body...)
	binary.LittleEndian.PutUint32(data[22:], oggCRC(data))
	return data
}
func head() []byte {
	h := make([]byte, 19)
	copy(h, "OpusHead")
	h[8] = 1
	h[9] = 2
	binary.LittleEndian.PutUint32(h[12:], 48000)
	return h
}
func TestOggSpanningPackets(t *testing.T) {
	stream := page(0, 2, []byte{19}, head())
	stream = append(stream, page(1, 0, []byte{8}, []byte("OpusTags"))...)
	frame := bytes.Repeat([]byte{0}, 300)
	frame[0] = 0xfc
	stream = append(stream, page(2, 0, []byte{255}, frame[:255])...)
	stream = append(stream, page(3, 5, []byte{45}, frame[255:])...)
	frames, e := ReadOpus(bytes.NewReader(stream))
	if e != nil {
		t.Fatal(e)
	}
	if len(frames) != 1 || !bytes.Equal(frames[0], frame) {
		t.Fatal("lacing reconstruction failed")
	}
	stream[len(stream)-1] ^= 1
	if _, e = ReadOpus(bytes.NewReader(stream)); e == nil {
		t.Fatal("checksum corruption accepted")
	}
}
func TestOggRejectsTruncatedAndWrongDuration(t *testing.T) {
	stream := page(0, 2, []byte{19}, head())
	stream = append(stream, page(1, 0, []byte{8}, []byte("OpusTags"))...)
	stream = append(stream, page(2, 4, []byte{1}, []byte{0xf4})...)
	if _, e := ReadOpus(bytes.NewReader(stream)); e == nil {
		t.Fatal("10ms packet accepted")
	}
	if _, e := ReadOpus(bytes.NewReader(stream[:len(stream)-1])); e == nil {
		t.Fatal("truncated Ogg accepted")
	}
}
func TestFFmpegRealPipeline(t *testing.T) {
	path := os.Getenv("FFMPEG_PATH")
	if path == "" {
		var e error
		path, e = exec.LookPath("ffmpeg")
		if e != nil {
			t.Skip("FFmpeg is not installed")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Generate MP3 from a deterministic sine fixture, then use the production conversion.
	cmd := exec.CommandContext(ctx, path, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-f", "mp3", "pipe:1")
	data, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	stream, e := NewFFmpeg(path).Convert(ctx, tts.AudioResult{MIME: "audio/mpeg", Stream: io.NopCloser(bytes.NewReader(data))})
	if e != nil {
		t.Fatal(e)
	}
	frames, e := collect(ctx, stream)
	if e != nil {
		t.Fatal(e)
	}
	if len(frames) < 10 || len(frames) > 20 {
		t.Fatalf("unexpected duration: %d frames", len(frames))
	}
	for _, f := range frames {
		if opusSamples(f) != 960 {
			t.Fatal("not 20ms")
		}
	}
	// Invalid MP3 must be reported instead of yielding empty successful playback.
	stream, e = NewFFmpeg(path).Convert(ctx, tts.AudioResult{MIME: "audio/mpeg", Stream: io.NopCloser(bytes.NewReader([]byte("invalid")))})
	if e == nil {
		_, e = collect(ctx, stream)
	}
	if e == nil {
		t.Fatal("invalid MP3 accepted")
	}
}
func TestFFmpegCancellationReapsProcess(t *testing.T) {
	path := t.TempDir() + "/blocked-converter"
	if e := os.WriteFile(path, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	stream, e := NewFFmpeg(path).Convert(ctx, tts.AudioResult{MIME: "audio/mpeg", Stream: io.NopCloser(bytes.NewReader([]byte("fixture")))})
	if e == nil {
		_, e = collect(ctx, stream)
	}
	if e == nil || time.Since(started) > 2*time.Second {
		t.Fatalf("converter not cancelled and reaped: %v", e)
	}
}
func TestFFmpegRealWAVPipeline(t *testing.T) {
	path := os.Getenv("FFMPEG_PATH")
	if path == "" {
		var err error
		path, err = exec.LookPath("ffmpeg")
		if err != nil {
			t.Skip("FFmpeg is not installed")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, path, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-f", "wav", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	stream, err := NewFFmpeg(path).Convert(ctx, tts.AudioResult{MIME: "audio/wav", Stream: io.NopCloser(bytes.NewReader(data))})
	if err != nil {
		t.Fatal(err)
	}
	frames, err := collect(ctx, stream)
	if err != nil || len(frames) < 10 || len(frames) > 20 {
		t.Fatalf("WAV conversion: %d frames, %v", len(frames), err)
	}
	for _, frame := range frames {
		if opusSamples(frame) != 960 {
			t.Fatal("not a 20ms packet")
		}
	}
}

func collect(ctx context.Context, s FrameStream) ([][]byte, error) {
	defer s.Close()
	var frames [][]byte
	for {
		f, e := s.Next(ctx)
		if e == io.EOF {
			return frames, nil
		}
		if e != nil {
			return nil, e
		}
		frames = append(frames, f)
	}
}

func TestFFmpegStartsBeforeMP3InputEnds(t *testing.T) {
	path := os.Getenv("FFMPEG_PATH")
	if path == "" {
		t.Skip("set FFMPEG_PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, path, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-b:a", "128k", "-f", "mp3", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer writer.Close()
	stream, err := NewFFmpeg(path).Convert(ctx, tts.AudioResult{MIME: "audio/mpeg", Stream: reader})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	// The rest of the MP3 and EOF remain unavailable until the first Opus frame.
	writeDone := make(chan error, 1)
	go func() { _, e := writer.Write(data[:8192]); writeDone <- e }()
	firstCtx, firstCancel := context.WithTimeout(ctx, 2*time.Second)
	defer firstCancel()
	start := time.Now()
	first, err := stream.Next(firstCtx)
	if err != nil {
		t.Fatalf("first packet waited for complete MP3: %v", err)
	}
	if opusSamples(first) != 960 {
		t.Fatal("not a 20ms packet")
	}
	t.Logf("first Opus packet before input EOF: %s", time.Since(start))
	select {
	case err = <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("input blocked")
	}
	go func() { _, e := writer.Write(data[8192:]); writer.CloseWithError(e) }()
	if _, err = collect(ctx, stream); err != nil {
		t.Fatal(err)
	}
}
