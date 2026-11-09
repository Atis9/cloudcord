package discordbot

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"cloudcord/internal/session"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
)

type idleTestConn struct {
	voice.Conn
	closed atomic.Bool
}

func (c *idleTestConn) Close(context.Context) { c.closed.Store(true) }

func TestEmptyVoiceTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const guild, self, first, last snowflake.ID = 1, 2, 3, 4
		channel, other := snowflake.ID(5), snowflake.ID(6)
		caches := cache.New(cache.WithCaches(cache.FlagVoiceStates))
		caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: self}})
		conn := &idleTestConn{}
		a := &active{conn: conn, speech: session.New(t.Context(), guild.String(), "7", channel.String(), nil, nil, nil)}
		defer a.speech.Stop()
		b := &Bot{ctx: t.Context(), Client: &bot.Client{Caches: caches}, sessions: map[snowflake.ID]*active{guild: a}, locks: map[snowflake.ID]*sync.Mutex{}}
		pending := func() bool {
			b.mu.Lock()
			defer b.mu.Unlock()
			return a.emptyTimer != nil
		}
		update := func(user snowflake.ID, destination *snowflake.ID) {
			old, _ := caches.VoiceState(guild, user)
			state := discord.VoiceState{GuildID: guild, UserID: user, ChannelID: destination}
			if destination == nil {
				caches.RemoveVoiceState(guild, user)
			} else {
				caches.AddVoiceState(state)
			}
			b.voiceState(&events.GuildVoiceStateUpdate{
				GenericGuildVoiceState: &events.GenericGuildVoiceState{VoiceState: state},
				OldVoiceState:          old,
			})
		}
		update(self, &channel)
		update(first, &channel)
		update(last, &channel)
		update(first, nil)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if conn.closed.Load() || pending() {
			t.Fatal("scheduled a disconnect with a participant still in the channel")
		}

		update(last, &other)
		time.Sleep(9 * time.Second)
		update(first, &channel)
		time.Sleep(time.Second)
		synctest.Wait()
		if conn.closed.Load() || pending() {
			t.Fatal("did not cancel the disconnect after a participant returned")
		}

		update(first, nil)
		time.Sleep(9 * time.Second)
		synctest.Wait()
		if conn.closed.Load() {
			t.Fatal("disconnected before the channel had been empty for ten seconds")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		b.mu.Lock()
		current := b.sessions[guild]
		b.mu.Unlock()
		if !conn.closed.Load() || current != nil {
			t.Fatal("did not clean up the empty voice session after ten seconds")
		}
	})
}
