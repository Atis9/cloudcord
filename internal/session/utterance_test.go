package session

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"cloudcord/internal/tts"
)

func TestMixedPostUsesOneRequestAndPlayback(t *testing.T) {
	for _, text := range []string{"読み上げの test を行います", strings.Repeat("APIをtestします。", 15)} {
		t.Run(text[:3], func(t *testing.T) {
			var calls atomic.Int32
			played := make(chan string, 2)
			s := New(context.Background(), "g", "t", "v", providerFunc(func(ctx context.Context, r tts.SpeechRequest) (tts.AudioResult, error) {
				calls.Add(1)
				return result(r.Text), nil
			}), converter{}, playerFunc(func(ctx context.Context, frames [][]byte) error {
				played <- string(frames[0])
				return nil
			}))
			defer s.Stop()
			s.Enqueue(text)
			if got := wait(t, played); got != text || calls.Load() != 1 {
				t.Fatal("mixed post was split or truncated before reaching Worker")
			}
		})
	}
}
