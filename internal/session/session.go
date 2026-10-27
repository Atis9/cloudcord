package session

import (
	"context"
	"fmt"
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

type Player interface {
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
	select {
	case s.queue <- utterance{text: text, enqueued: time.Now()}:
		return true
	default:
		s.Dropped.Add(1)
		slog.Warn("queue_full", "guild_id", s.GuildID, "queue_length", len(s.queue), "dropped", s.Dropped.Load())
		return false
	}
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
		for {
			select {
			case <-s.queue:
			default:
				return
			}
		}
	}()
	for {
		select {
		case <-s.ctx.Done():
			return
		case job := <-s.queue:
			if s.ctx.Err() != nil {
				return
			}
			started := time.Now()
			ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
			err := s.playUtterance(ctx, job, started, p, c, player)
			cancel()
			if err != nil {
				slog.Warn("speech_failed", "guild_id", s.GuildID, "error", err)
			} else {
				slog.Info("speech_complete", "guild_id", s.GuildID, "duration_ms", time.Since(started).Milliseconds(), "queue_length", len(s.queue))
			}
		}
	}
}

// Synthesize the complete post once; kana conversion lives in the Worker.
func (s *GuildSession) playUtterance(ctx context.Context, job utterance, started time.Time, p tts.Provider, c audio.Converter, player Player) error {
	ttsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := p.Synthesize(ttsCtx, tts.SpeechRequest{GuildID: s.GuildID, Text: job.text, Language: "ja"})
	if err != nil {
		return fmt.Errorf("TTS failed: %w", err)
	}
	slog.Info("tts_stream_ready", "guild_id", s.GuildID, "request_id", result.RequestID, "duration_ms", time.Since(started).Milliseconds())
	frames, err := c.Convert(ctx, result)
	if err != nil {
		return err
	}
	err = player.Play(ctx, frames, func() {
		slog.Info("speech_started", "guild_id", s.GuildID, "request_id", result.RequestID, "queue_wait_ms", started.Sub(job.enqueued).Milliseconds(), "first_packet_ms", time.Since(job.enqueued).Milliseconds(), "pipeline_start_ms", time.Since(started).Milliseconds())
	})
	closeErr := frames.Close()
	if err != nil {
		return err
	}
	return closeErr
}
