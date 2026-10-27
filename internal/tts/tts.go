package tts

import (
	"context"
	"io"
)

const MaxAudioBytes = 5 << 20

type SpeechRequest struct {
	GuildID  string `json:"guild_id"`
	Text     string `json:"text"`
	Language string `json:"language"`
}
type AudioResult struct {
	MIME      string
	Stream    io.ReadCloser
	RequestID string
}
type Provider interface {
	Synthesize(context.Context, SpeechRequest) (AudioResult, error)
}
