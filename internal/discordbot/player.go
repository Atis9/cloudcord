package discordbot

import (
	"cloudcord/internal/audio"
	"context"
	"io"
	"time"

	"github.com/disgoorg/disgo/voice"
)

type voicePlayer struct{ conn voice.Conn }

func (p voicePlayer) Play(ctx context.Context, frames audio.FrameStream, started func()) error {
	if err := p.conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone); err != nil {
		return err
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.conn.SetSpeaking(stop, voice.SpeakingFlagNone)
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	first := true
	// DisGo's UDP writer applies both DAVE and transport encryption.
	send := func(frame []byte) error {
		if !first {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
		deadline := time.Now().Add(time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err := p.conn.UDP().SetWriteDeadline(deadline); err != nil {
			return err
		}
		_, err := p.conn.UDP().Write(frame)
		if err == nil {
			ticker.Reset(20 * time.Millisecond)
		}
		if err == nil && first {
			first = false
			if started != nil {
				started()
			}
		}
		return err
	}
	for {
		frame, err := frames.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err = send(frame); err != nil {
			return err
		}
	}
	for i := 0; i < 5; i++ {
		if err := send(voice.SilenceAudioFrame); err != nil {
			return err
		}
	}
	return nil
}
