package discordbot

import (
	"context"
	"io"
	"log/slog"
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
	const frameInterval = time.Duration(voice.OpusFrameSizeMs) * time.Millisecond
	timer := time.NewTimer(0)
	defer timer.Stop()
	start := time.Now()
	var nextSend, lastSend time.Time
	var firstGapAt, maxGap time.Duration
	var gaps int
	defer func() {
		if gaps > 0 {
			slog.Warn("voice_packet_gaps", "count", gaps, "first_gap_at_ms", firstGapAt.Milliseconds(), "max_gap_ms", maxGap.Milliseconds())
		}
	}()
	// DisGo's UDP writer applies both DAVE and transport encryption.
	send := func(frame []byte) error {
		if delay := time.Until(nextSend); delay > 0 {
			timer.Reset(delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		udp := p.conn.UDP()
		deadline := time.Now().Add(time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err := udp.SetWriteDeadline(deadline); err != nil {
			return err
		}
		sentAt := time.Now()
		if _, err := udp.Write(frame); err != nil {
			return err
		}
		if gap := sentAt.Sub(lastSend); !lastSend.IsZero() && gap >= 2*frameInterval {
			if gaps == 0 {
				firstGapAt = sentAt.Sub(start)
			}
			gaps++
			maxGap = max(maxGap, gap)
		}
		lastSend = sentAt
		// Follow the audio clock so encryption and UDP writes do not add to
		// every interval. Rebase after a stall to avoid sending a burst.
		if sentAt.Sub(nextSend) > frameInterval/2 {
			nextSend = sentAt
		}
		nextSend = nextSend.Add(frameInterval)
		return nil
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
