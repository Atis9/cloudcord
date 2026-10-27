package tts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Cloudflare struct {
	base, token  string
	client       *http.Client
	mu           sync.Mutex
	blockedUntil time.Time
}

func NewCloudflare(base, token string) (*Cloudflare, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("CLOUDFLARE_API_BASE_URL must be an HTTPS URL")
	}
	if token == "" {
		return nil, errors.New("CLOUDFLARE_BOT_API_TOKEN is required")
	}
	return &Cloudflare{base: strings.TrimRight(base, "/"), token: token, client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Cloudflare) Synthesize(ctx context.Context, speech SpeechRequest) (AudioResult, error) {
	c.mu.Lock()
	blocked := time.Now().Before(c.blockedUntil)
	c.mu.Unlock()
	if blocked {
		return AudioResult{}, errors.New("TTS rate limit cooldown")
	}
	payload, err := json.Marshal(speech)
	if err != nil {
		return AudioResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/tts", bytes.NewReader(payload))
	if err != nil {
		return AudioResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg, audio/wav")
	resp, err := c.client.Do(req)
	if err != nil {
		return AudioResult{}, errors.New("TTS HTTP request failed")
	}
	accepted := false
	defer func() {
		if !accepted {
			resp.Body.Close()
		}
	}()
	if resp.StatusCode == http.StatusTooManyRequests {
		until := time.Now().Add(time.Minute)
		if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds >= 0 && seconds <= 86400 {
			until = time.Now().Add(time.Duration(seconds) * time.Second)
		} else if date, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil && date.After(time.Now()) {
			until = date
		}
		c.mu.Lock()
		if until.After(c.blockedUntil) {
			c.blockedUntil = until
		}
		c.mu.Unlock()
	}
	if resp.StatusCode != http.StatusOK {
		return AudioResult{}, fmt.Errorf("TTS HTTP status %d", resp.StatusCode)
	}
	kind, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || (kind != "audio/mpeg" && kind != "audio/wav") {
		return AudioResult{}, errors.New("TTS returned unsupported content type")
	}
	if resp.ContentLength > MaxAudioBytes {
		return AudioResult{}, errors.New("TTS audio exceeds 5 MiB")
	}

	body := LimitAudio(resp.Body)
	buffered := bufio.NewReader(body)
	if _, err = buffered.Peek(1); err != nil {
		return AudioResult{}, errors.New("empty or unreadable TTS audio")
	}
	accepted = true
	return AudioResult{MIME: kind, Stream: &bufferedBody{Reader: buffered, Closer: body}, RequestID: resp.Header.Get("X-Request-ID")}, nil
}

type bufferedBody struct {
	io.Reader
	io.Closer
}
type boundedBody struct {
	source    io.ReadCloser
	remaining int
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if len(p) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.source.Read(p)
	if n > b.remaining {
		return 0, errors.New("TTS audio exceeds 5 MiB")
	}
	b.remaining -= n
	if err != nil && err != io.EOF {
		return n, errors.New("TTS audio read failed")
	}
	return n, err
}
func (b *boundedBody) Close() error { return b.source.Close() }

// LimitAudio enforces the same byte cap for provider and converter streams.
func LimitAudio(stream io.ReadCloser) io.ReadCloser {
	return &boundedBody{source: stream, remaining: MaxAudioBytes}
}
