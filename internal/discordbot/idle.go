package discordbot

import (
	"log/slog"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

const emptyVoiceTimeout = 10 * time.Second

// Caller holds b.mu.
func (a *active) cancelEmptyTimer() {
	if a.emptyTimer != nil {
		a.emptyTimer.Stop()
		a.emptyTimer = nil
	}
}

func (b *Bot) voiceChannelOccupied(guild snowflake.ID, a *active) bool {
	channel := snowflake.MustParse(a.speech.VoiceChannelID)
	self := b.Client.ID()
	for state := range b.Client.Caches.VoiceStates(guild) {
		if state.UserID != self && state.ChannelID != nil && *state.ChannelID == channel {
			return true
		}
	}
	return false
}

// Caller holds b.mu. DisGo updates the voice-state cache before dispatching events.
func (b *Bot) updateEmptyTimer(guild snowflake.ID, a *active) {
	if b.ctx.Err() != nil || b.voiceChannelOccupied(guild, a) {
		a.cancelEmptyTimer()
		return
	}
	if a.emptyTimer != nil {
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(emptyVoiceTimeout, func() {
		lock := b.guildLock(guild)
		lock.Lock()
		defer lock.Unlock()

		b.mu.Lock()
		if b.sessions[guild] != a || a.emptyTimer != timer || a.recovering || b.closing || b.ctx.Err() != nil {
			b.mu.Unlock()
			return
		}
		a.emptyTimer = nil
		if b.voiceChannelOccupied(guild, a) {
			b.mu.Unlock()
			return
		}
		delete(b.sessions, guild)
		b.commands.Add(1)
		b.mu.Unlock()
		defer b.commands.Done()

		slog.Info("voice_idle_timeout", "guild_id", guild.String())
		closeVoiceSession(guild, a)
	})
	a.emptyTimer = timer
}
