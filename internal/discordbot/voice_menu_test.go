package discordbot

import (
	"strings"
	"testing"

	"cloudcord/internal/session"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

func TestAllVoicesAppearInMenus(t *testing.T) {
	b := &Bot{sessions: map[snowflake.ID]*active{1: {speech: &session.GuildSession{}}}}
	b.sessions[1].speech.SetSpeaker("Sulafat")
	rows := b.voiceMenus(1)
	if len(rows) != 2 {
		t.Fatal("expected two voice menus")
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, row := range rows {
		menu := row.(discord.ActionRowComponent).Components[0].(discord.StringSelectMenuComponent)
		if len(menu.Options) != 15 || ids[menu.CustomID] {
			t.Fatal("menus must have fifteen options and distinct custom IDs")
		}
		ids[menu.CustomID] = true
		for _, option := range menu.Options {
			if seen[option.Value] || option.Label != option.Value || option.Default != (option.Value == "Sulafat") {
				t.Fatal("voice options must be unique, named and show the current selection")
			}
			seen[option.Value] = true
		}
	}
	if len(seen) != 30 {
		t.Fatal("not all thirty voices were shown")
	}
}

func TestVoiceMenuChecksChannelAndSession(t *testing.T) {
	const guild, user snowflake.ID = 1, 2
	channel := snowflake.ID(3)
	caches := cache.New(cache.WithCaches(cache.FlagVoiceStates))
	caches.AddVoiceState(discord.VoiceState{GuildID: guild, UserID: user, ChannelID: &channel})
	b := &Bot{Client: &bot.Client{Caches: caches}, sessions: map[snowflake.ID]*active{
		guild: {speech: &session.GuildSession{VoiceChannelID: channel.String()}},
	}}
	menu := b.voiceMenus(guild)[0].(discord.ActionRowComponent).Components[0].(discord.StringSelectMenuComponent)
	b.selectVoice(guild, user, menu.CustomID, "Puck")
	if b.sessions[guild].speech.Speaker() != "Puck" {
		t.Fatal("could not select a voice outside the original three")
	}
	caches.RemoveVoiceState(guild, user)
	b.selectVoice(guild, user, menu.CustomID, "Charon")
	if b.sessions[guild].speech.Speaker() != "Puck" {
		t.Fatal("a user outside the voice channel changed the speaker")
	}
	caches.AddVoiceState(discord.VoiceState{GuildID: guild, UserID: user, ChannelID: &channel})
	b.sessions[guild] = &active{speech: &session.GuildSession{VoiceChannelID: channel.String()}}
	if result := b.selectVoice(guild, user, menu.CustomID, "Charon"); !strings.Contains(result, "終了") {
		t.Fatal("old menu must not change a new session")
	}
	if b.sessions[guild].speech.Speaker() != "" {
		t.Fatal("a stale menu changed the new session")
	}
}
