package discordbot

import (
	"encoding/json"
	"testing"

	"cloudcord/internal/session"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

func TestCommandRegistrationClearsAdminRequirement(t *testing.T) {
	for _, command := range commandDefinitions() {
		encoded, err := json.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]json.RawMessage
		if err = json.Unmarshal(encoded, &data); err != nil {
			t.Fatal(err)
		}
		if string(data["default_member_permissions"]) != "null" {
			t.Fatalf("must explicitly clear existing admin requirement: %s", encoded)
		}
	}
}

func TestVoiceControlRequiresSameChannel(t *testing.T) {
	const guild, user snowflake.ID = 1, 2
	caches := cache.New(cache.WithCaches(cache.FlagVoiceStates))
	b := &Bot{Client: &bot.Client{Caches: caches}, sessions: map[snowflake.ID]*active{}}
	if b.controlError(guild, user) == "" {
		t.Fatal("allowed user outside voice")
	}
	channel := snowflake.ID(3)
	caches.AddVoiceState(discord.VoiceState{GuildID: guild, UserID: user, ChannelID: &channel})
	if reason := b.controlError(guild, user); reason != "" {
		t.Fatal(reason)
	}
	b.sessions[guild] = &active{speech: &session.GuildSession{VoiceChannelID: "4"}}
	if b.controlError(guild, user) == "" {
		t.Fatal("allowed control from another voice channel")
	}
	b.sessions[guild].speech.VoiceChannelID = "3"
	if reason := b.controlError(guild, user); reason != "" {
		t.Fatal(reason)
	}
}
