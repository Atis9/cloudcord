package discordbot

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"cloudcord/internal/session"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

func TestPlaybackSpeedCommand(t *testing.T) {
	b := &Bot{sessions: map[snowflake.ID]*active{
		1: {speech: &session.GuildSession{}},
		2: {speech: &session.GuildSession{}},
	}}
	if result := b.speed(3, 1.5, true); !strings.Contains(result, "/join") {
		t.Fatal("speed selection should require an active session")
	}
	if result := b.speed(1, 0, false); !strings.Contains(result, "1倍") {
		t.Fatal("default playback speed should be normal")
	}
	b.speed(1, 0.5, true)
	b.speed(2, 2, true)
	for _, value := range []float64{0, 0.49, 2.01, math.NaN(), math.Inf(1)} {
		b.speed(1, value, true)
	}
	if b.sessions[1].speech.Speed() != 0.5 || b.sessions[2].speech.Speed() != 2 {
		t.Fatal("invalid speed or another guild changed the session speed")
	}
}

func TestVoiceSpeakerSelectionIsScopedToActiveSession(t *testing.T) {
	const first, second snowflake.ID = 1, 2
	b := &Bot{sessions: map[snowflake.ID]*active{first: {speech: &session.GuildSession{}}}}
	if result := b.speaker(second, "Kore", true); !strings.Contains(result, "/join") {
		t.Fatal("speaker selection should require an active session")
	}
	b.sessions[second] = &active{speech: &session.GuildSession{}}
	b.speaker(first, "Kore", true)
	b.speaker(first, "unavailable", true)
	if result := b.speaker(first, "", false); !strings.Contains(result, "Kore") {
		t.Fatalf("current speaker was not reported: %s", result)
	}
	if got := b.sessions[second].speech.Speaker(); got != "" {
		t.Fatalf("speaker leaked into another guild: %s", got)
	}
}

func TestModelSelectionIsScopedToActiveSession(t *testing.T) {
	b := &Bot{sessions: map[snowflake.ID]*active{
		1: {speech: &session.GuildSession{}},
		2: {speech: &session.GuildSession{}},
	}}
	if result := b.model(3, "gemini", true); !strings.Contains(result, "/join") {
		t.Fatal("model selection should require an active session")
	}
	b.speaker(1, "Kore", true)
	b.model(1, "gemini", true)
	b.model(1, "invalid", true)
	if result := b.model(1, "", false); !strings.Contains(result, "Gemini Flash TTS") {
		t.Fatalf("selected model was not reported: %s", result)
	}
	if b.sessions[1].speech.Engine() != "gemini" || b.sessions[1].speech.Speaker() != "Kore" || b.sessions[2].speech.Engine() != "" {
		t.Fatal("model choice changed another guild or the selected voice")
	}
	b.model(1, "gemini-lite", true)
	if b.sessions[1].speech.Engine() != "gemini-lite" {
		t.Fatal("could not switch back to Lite")
	}
}

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
