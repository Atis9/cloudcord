package session

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"cloudcord/internal/audio"
	"cloudcord/internal/tts"
)

const QueueLimit = 20

type utterance struct {
	text     string
	enqueued time.Time
}

type nextSpeech struct {
	job        utterance
	prefetched bool
	audio      tts.AudioResult
	err        error
}

type Player interface {
	// Call started synchronously when the first packet is sent.
	Play(context.Context, audio.FrameStream, func()) error
}
type GuildSession struct {
	GuildID, TextChannelID, VoiceChannelID string
	ctx                                    context.Context
	cancel                                 context.CancelFunc
	queue                                  chan utterance
	done                                   chan struct{}
	mu                                     sync.Mutex
	closed                                 bool
	waiting                                int // Includes the next post while it is being prefetched.
	Dropped                                atomic.Uint64
}

func New(parent context.Context, guild, text, voice string, p tts.Provider, c audio.Converter, player Player) *GuildSession {
	ctx, cancel := context.WithCancel(parent)
	s := &GuildSession{GuildID: guild, TextChannelID: text, VoiceChannelID: voice, ctx: ctx, cancel: cancel, queue: make(chan utterance, QueueLimit), done: make(chan struct{})}
	go s.run(p, c, player)
	return s
}
func (s *GuildSession) Enqueue(text string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil || text == "" {
		return false
	}
	if s.waiting == QueueLimit {
		s.Dropped.Add(1)
		slog.Warn("queue_full", "guild_id", s.GuildID, "queue_length", s.waiting, "dropped", s.Dropped.Load())
		return false
	}
	// The channel cannot be full when the total waiting count is below its capacity.
	s.queue <- utterance{text: text, enqueued: time.Now()}
	s.waiting++
	return true
}
func (s *GuildSession) Stop() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	<-s.done
}
func (s *GuildSession) run(p tts.Provider, c audio.Converter, player Player) {
	defer close(s.done)
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.waiting = 0
		for {
			select {
			case <-s.queue:
			default:
				return
			}
		}
	}()
	var next *nextSpeech
	for {
		if next == nil {
			select {
			case <-s.ctx.Done():
				return
			case job := <-s.queue:
				next = &nextSpeech{job: job}
			}
		}
		if s.ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		s.waiting--
		s.mu.Unlock()
		started := time.Now()
		ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
		var pending <-chan *nextSpeech
		var stopWaiting context.CancelFunc
		err := s.playUtterance(ctx, next, started, p, c, player, func() {
			if pending == nil {
				pending, stopWaiting = s.prefetch(p)
			}
		})
		cancel()
		if stopWaiting != nil {
			stopWaiting()
		}
		if err != nil {
			slog.Warn("speech_failed", "guild_id", s.GuildID, "error", err)
		} else {
			s.mu.Lock()
			waiting := s.waiting
			s.mu.Unlock()
			slog.Info("speech_complete", "guild_id", s.GuildID, "duration_ms", time.Since(started).Milliseconds(), "queue_length", waiting)
		}
		next = nil
		if pending != nil {
			next = <-pending
		}
	}
}

// Wait for one post only while playback is active. Once synthesis starts it
// belongs to the guild, so finishing the previous post does not cancel it.
func (s *GuildSession) prefetch(p tts.Provider) (<-chan *nextSpeech, context.CancelFunc) {
	waitCtx, stopWaiting := context.WithCancel(s.ctx)
	pending := make(chan *nextSpeech, 1)
	go func() {
		defer close(pending)
		var job utterance
		select {
		case <-waitCtx.Done():
			return
		case job = <-s.queue:
		}
		next := &nextSpeech{job: job}
		// If playback ended as the job arrived, preserve FIFO but use the
		// normal streaming path instead of waiting for a complete download.
		if waitCtx.Err() == nil {
			ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
			defer cancel()
			next.prefetched = true
			next.audio, next.err = s.synthesize(ctx, job, p, true)
			if next.err == nil {
				body := tts.LimitAudio(next.audio.Stream)
				data, err := io.ReadAll(body)
				_ = body.Close()
				if err != nil {
					next.err = fmt.Errorf("prefetch audio: %w", err)
				} else {
					// The HTTP deadline can expire before this post's turn.
					next.audio.Stream = io.NopCloser(bytes.NewReader(data))
				}
			}
		}
		pending <- next
	}()
	return pending, stopWaiting
}

func (s *GuildSession) synthesize(ctx context.Context, job utterance, p tts.Provider, prefetched bool) (tts.AudioResult, error) {
	started := time.Now()
	result, err := p.Synthesize(ctx, tts.SpeechRequest{GuildID: s.GuildID, Text: job.text, Language: "ja"})
	if err != nil {
		return tts.AudioResult{}, fmt.Errorf("TTS failed: %w", err)
	}
	slog.Info("tts_stream_ready", "guild_id", s.GuildID, "request_id", result.RequestID, "duration_ms", time.Since(started).Milliseconds(), "prefetched", prefetched)
	return result, nil
}

func (s *GuildSession) playUtterance(ctx context.Context, next *nextSpeech, started time.Time, p tts.Provider, c audio.Converter, player Player, onStart func()) error {
	result, err := next.audio, next.err
	if !next.prefetched {
		ttsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		result, err = s.synthesize(ttsCtx, next.job, p, false)
	}
	if err != nil {
		return err
	}
	frames, err := c.Convert(ctx, result)
	if err != nil {
		return err
	}
	err = player.Play(ctx, frames, func() {
		slog.Info("speech_started", "guild_id", s.GuildID, "request_id", result.RequestID, "queue_wait_ms", started.Sub(next.job.enqueued).Milliseconds(), "first_packet_ms", time.Since(next.job.enqueued).Milliseconds(), "pipeline_start_ms", time.Since(started).Milliseconds(), "prefetched", next.prefetched)
		onStart()
	})
	closeErr := frames.Close()
	if err != nil {
		return err
	}
	return closeErr
}
