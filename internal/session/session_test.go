package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"cloudcord/internal/audio"
	"cloudcord/internal/tts"
)

type providerFunc func(context.Context, tts.SpeechRequest) (tts.AudioResult, error)

func (f providerFunc) Synthesize(c context.Context, r tts.SpeechRequest) (tts.AudioResult, error) {
	return f(c, r)
}

type converter struct{}

func (converter) Convert(ctx context.Context, a tts.AudioResult) (audio.FrameStream, error) {
	defer a.Stream.Close()
	b, e := io.ReadAll(a.Stream)
	return &testFrames{data: [][]byte{b}}, e
}

type testFrames struct{ data [][]byte }

func (f *testFrames) Next(ctx context.Context) ([]byte, error) {
	if len(f.data) == 0 {
		return nil, io.EOF
	}
	p := f.data[0]
	f.data = f.data[1:]
	return p, nil
}
func (f *testFrames) Close() error { return nil }

type playerFunc func(context.Context, [][]byte) error

func (f playerFunc) Play(ctx context.Context, stream audio.FrameStream, started func()) error {
	var frames [][]byte
	for {
		frame, err := stream.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		frames = append(frames, frame)
	}
	if started != nil {
		started()
	}
	return f(ctx, frames)
}
func result(text string) tts.AudioResult {
	return tts.AudioResult{MIME: "audio/mpeg", Stream: io.NopCloser(bytes.NewBufferString(text))}
}
func wait(t *testing.T, c <-chan string) string {
	t.Helper()
	select {
	case s := <-c:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
		return ""
	}
}
func TestFIFOContinuesAfterFailure(t *testing.T) {
	calls := make(chan string, 10)
	played := make(chan string, 10)
	p := providerFunc(func(ctx context.Context, r tts.SpeechRequest) (tts.AudioResult, error) {
		calls <- r.Text
		if r.Text == "bad" {
			return tts.AudioResult{}, errors.New("upstream failed")
		}
		return result(r.Text), nil
	})
	s := New(context.Background(), "guild", "text", "voice", p, converter{}, playerFunc(func(ctx context.Context, b [][]byte) error { played <- string(b[0]); return nil }))
	defer s.Stop()
	for _, v := range []string{"first", "bad", "last"} {
		s.Enqueue(v)
	}
	for _, v := range []string{"first", "bad", "last"} {
		if got := wait(t, calls); got != v {
			t.Fatalf("FIFO: %s != %s", got, v)
		}
	}
	for _, v := range []string{"first", "last"} {
		if got := wait(t, played); got != v {
			t.Fatalf("playback: %s != %s", got, v)
		}
	}
}
func TestQueueBoundAndStopCancelsHTTP(t *testing.T) {
	started := make(chan string, 1)
	s := New(context.Background(), "g", "t", "v", providerFunc(func(ctx context.Context, r tts.SpeechRequest) (tts.AudioResult, error) {
		started <- r.Text
		<-ctx.Done()
		return tts.AudioResult{}, ctx.Err()
	}), converter{}, playerFunc(func(context.Context, [][]byte) error { t.Error("unexpected playback"); return nil }))
	s.Enqueue("active")
	wait(t, started)
	for i := 0; i < QueueLimit; i++ {
		if !s.Enqueue("queued") {
			t.Fatal("queue filled too early")
		}
	}
	if s.Enqueue("overflow") || s.Dropped.Load() != 1 {
		t.Fatal("unbounded queue")
	}
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel provider")
	}
	if s.Enqueue("after leave") || len(s.queue) != 0 {
		t.Fatal("queue not discarded")
	}
	s.Stop()
}
func TestGuildIsolationAndPlaybackCancellation(t *testing.T) {
	started := make(chan string, 2)
	p := providerFunc(func(ctx context.Context, r tts.SpeechRequest) (tts.AudioResult, error) { return result(r.GuildID), nil })
	makeSession := func(id string) *GuildSession {
		return New(context.Background(), id, "t", "v", p, converter{}, playerFunc(func(ctx context.Context, b [][]byte) error { started <- string(b[0]); <-ctx.Done(); return ctx.Err() }))
	}
	a, b := makeSession("a"), makeSession("b")
	defer b.Stop()
	a.Enqueue("a")
	b.Enqueue("b")
	seen := map[string]bool{wait(t, started): true, wait(t, started): true}
	if !seen["a"] || !seen["b"] {
		t.Fatal("guilds not isolated")
	}
	a.Stop()
	if b.ctx.Err() != nil {
		t.Fatal("stopping one guild cancelled another")
	}
}
