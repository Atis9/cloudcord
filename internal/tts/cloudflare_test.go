package tts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func clientFor(t *testing.T, h http.HandlerFunc) *Cloudflare {
	t.Helper()
	s := httptest.NewTLSServer(h)
	t.Cleanup(s.Close)
	c, e := NewCloudflare(s.URL, "secret")
	if e != nil {
		t.Fatal(e)
	}
	redirectPolicy := c.client.CheckRedirect
	c.client = s.Client()
	c.client.CheckRedirect = redirectPolicy
	c.client.Timeout = time.Second
	return c
}
func TestCloudflareContract(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tts" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("bad request")
		}
		var v SpeechRequest
		if e := json.NewDecoder(r.Body).Decode(&v); e != nil || v.GuildID != "123456789012345678" || v.Language != "ja" || v.Text != "こんにちは" {
			t.Error("invalid payload")
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("X-Request-ID", "req-1")
		_, _ = w.Write([]byte("ID3fixture"))
	})
	a, e := c.Synthesize(context.Background(), SpeechRequest{"123456789012345678", "こんにちは", "ja"})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Stream.Close()
	b, _ := io.ReadAll(a.Stream)
	if string(b) != "ID3fixture" || a.RequestID != "req-1" {
		t.Fatal("bad audio result")
	}
}
func TestCloudflareRejectsBadResponses(t *testing.T) {
	for _, kind := range []string{"type", "size", "empty", "upstream", "redirect"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "audio/mpeg")
				switch kind {
				case "type":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte("{}"))
				case "size":
					_, _ = io.Copy(w, strings.NewReader(strings.Repeat("x", MaxAudioBytes+1)))
				case "empty":
				case "upstream":
					http.Error(w, "sensitive upstream body", 502)
				case "redirect":
					http.Redirect(w, r, "/other", 302)
				}
			})
			a, e := c.Synthesize(context.Background(), SpeechRequest{"12", "a", "ja"})
			if e == nil {
				_, e = io.ReadAll(a.Stream)
				a.Stream.Close()
			}
			if e == nil || strings.Contains(e.Error(), "sensitive") {
				t.Fatalf("expected sanitized failure: %v", e)
			}
			if calls != 1 {
				t.Fatal("request retried or redirect followed")
			}
		})
	}
}
func TestCloudflareWAVContract(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), "audio/wav") {
			t.Error("WAV not accepted")
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("RIFF1234WAVEfixture"))
	})
	a, err := c.Synthesize(context.Background(), SpeechRequest{"12", "こんにちは", "ja"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stream.Close()
	data, err := io.ReadAll(a.Stream)
	if err != nil || a.MIME != "audio/wav" || string(data) != "RIFF1234WAVEfixture" {
		t.Fatalf("WAV contract failed: %s, %v", a.MIME, err)
	}
}
func TestCloudflareRateLimitCooldown(t *testing.T) {
	calls := 0
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	})
	for i := 0; i < 2; i++ {
		if _, e := c.Synthesize(context.Background(), SpeechRequest{"12", "a", "ja"}); e == nil {
			t.Fatal("expected failure")
		}
	}
	if calls != 1 {
		t.Fatal("cooldown ignored")
	}
}
func TestCloudflareCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) { close(started); <-release })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := c.Synthesize(ctx, SpeechRequest{"12", "a", "ja"}); done <- e }()
	<-started
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("expected cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP request did not cancel")
	}
}

func TestCloudflareStartsBeforeHTTPBodyEnds(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte("ID3prefix"))
		w.(http.Flusher).Flush()
		<-release
		w.Write([]byte("tail"))
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a, err := c.Synthesize(ctx, SpeechRequest{"12", "a", "ja"})
	if err != nil {
		t.Fatal("waited for full HTTP body:", err)
	}
	defer a.Stream.Close()
	first := make([]byte, 9)
	if _, err = io.ReadFull(a.Stream, first); err != nil || string(first) != "ID3prefix" {
		t.Fatal("no first chunk:", err)
	}
}
