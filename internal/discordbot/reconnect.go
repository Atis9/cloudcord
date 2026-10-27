package discordbot

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"cloudcord/internal/session"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave"
	"github.com/disgoorg/snowflake/v2"
	"github.com/gorilla/websocket"
)

func (b *Bot) createVoiceConn(guild, user snowflake.ID, update voice.StateUpdateFunc, remove func(), opts ...voice.ConnConfigOpt) voice.Conn {
	return voice.NewConn(guild, user, update, remove, append(opts, voice.WithConnGatewayCreateFunc(
		func(dave godave.Session, events voice.EventHandlerFunc, closed voice.CloseHandlerFunc, gatewayOpts ...voice.GatewayConfigOpt) voice.Gateway {
			return voice.NewGateway(dave, events, func(g voice.Gateway, err error, reconnect bool) {
				b.voiceClosed(guild, g, err, reconnect, closed)
			}, gatewayOpts...)
		}))...)
}

// DisGo resumes transient failures itself. An expired session needs a new
// voice-state handshake, after the old connection has been fully removed.
func (b *Bot) voiceClosed(guild snowflake.ID, g voice.Gateway, err error, reconnect bool, closed voice.CloseHandlerFunc) {
	var closeErr *websocket.CloseError
	expired := errors.As(err, &closeErr) && (closeErr.Code == 4006 || closeErr.Code == 4009)
	b.mu.Lock()
	a := b.sessions[guild]
	recover := expired && !b.closing && b.ctx.Err() == nil && a != nil && !a.recovering && a.conn.Gateway() == g
	if recover {
		a.recovering = true
		b.commands.Add(1)
	}
	b.mu.Unlock()
	// Preserve the library's UDP, Gateway and manager cleanup.
	closed(g, err, reconnect)
	if !recover {
		return
	}
	defer b.commands.Done()
	lock := b.guildLock(guild)
	lock.Lock()
	defer lock.Unlock()
	b.mu.Lock()
	same := b.sessions[guild] == a
	b.mu.Unlock()
	if !same || b.ctx.Err() != nil {
		return
	}
	a.speech.Stop()
	b.mu.Lock()
	delete(b.sessions, guild)
	b.mu.Unlock()
	slog.Warn("voice_session_expired", "guild_id", guild.String(), "code", closeErr.Code)
	conn := b.Client.VoiceManager.CreateConn(guild)
	ctx, cancel := context.WithTimeout(b.ctx, 15*time.Second)
	defer cancel()
	if err := conn.Open(ctx, snowflake.MustParse(a.speech.VoiceChannelID), false, true); err != nil || b.ctx.Err() != nil {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn.Close(stop)
		slog.Warn("voice_rejoin_failed", "guild_id", guild.String())
		return // Leave no stale session; /join can retry.
	}
	next := &active{conn: conn, speech: session.New(b.ctx, guild.String(), a.speech.TextChannelID, a.speech.VoiceChannelID, b.tts, b.converter, voicePlayer{conn})}
	b.mu.Lock()
	b.sessions[guild] = next
	b.mu.Unlock()
	slog.Info("voice_rejoined", "guild_id", guild.String())
}
