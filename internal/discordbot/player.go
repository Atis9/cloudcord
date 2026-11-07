package discordbot

import (
	"context"
	"io"
	"time"

	"cloudcord/internal/audio"
	"github.com/disgoorg/disgo/voice"
)

type voicePlayer struct{ conn voice.Conn }

func (p voicePlayer) Play(ctx context.Context, frames audio.FrameStream, started func()) error {
	// Let the converter get slightly ahead before sending the first audio packet.
	var buffered [3][]byte
	count := 0
	for count < len(buffered) {
		frame, err := frames.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		buffered[count] = frame
		count++
	}
	if count == 0 || ctx.Err() != nil {
		return ctx.Err()
	}
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
	sent := false
	// DisGo's UDP writer applies both DAVE and transport encryption.
	send := func(frame []byte) error {
		if sent {
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
			sent = true
			ticker.Reset(20 * time.Millisecond)
		}
		return err
	}
	// Give the speaking update and receiver a short lead before audible frames.
	for i := 0; i < 2; i++ {
		if err := send(voice.SilenceAudioFrame); err != nil {
			return err
		}
	}
	for i, frame := range buffered[:count] {
		if err := send(frame); err != nil {
			return err
		}
		if i == 0 && started != nil {
			started()
		}
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
