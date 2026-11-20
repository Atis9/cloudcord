package discordbot

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// Caller holds the guild command lock.
func (b *Bot) voiceMenus(guild snowflake.ID) []discord.LayoutComponent {
	b.mu.Lock()
	current := b.sessions[guild]
	if current == nil {
		b.mu.Unlock()
		return nil
	}
	if current.voiceMenuKey == "" {
		current.voiceMenuKey = rand.Text()
	}
	key := current.voiceMenuKey
	b.mu.Unlock()
	selected := current.speech.Speaker()
	var rows []discord.LayoutComponent
	for start := 0; start < len(speakerChoices); start += 15 {
		end := min(start+15, len(speakerChoices))
		var options []discord.StringSelectMenuOption
		for _, choice := range speakerChoices[start:end] {
			options = append(options, discord.NewStringSelectMenuOption(choice.Name, choice.Value).WithDefault(choice.Value == selected))
		}
		menu := discord.NewStringSelectMenu(
			fmt.Sprintf("cloudcord-voice:%s:%d", key, start),
			fmt.Sprintf("%s 〜 %s", speakerChoices[start].Name, speakerChoices[end-1].Name),
			options...,
		)
		rows = append(rows, discord.NewActionRow(menu))
	}
	return rows
}

// Caller holds the guild command lock.
func (b *Bot) selectVoice(guild, user snowflake.ID, customID, speaker string) string {
	if reason := b.controlError(guild, user); reason != "" {
		return reason
	}
	b.mu.Lock()
	current := b.sessions[guild]
	valid := current != nil && current.voiceMenuKey != "" && strings.HasPrefix(customID, "cloudcord-voice:"+current.voiceMenuKey+":")
	b.mu.Unlock()
	if !valid {
		return "この選択メニューのセッションは終了しました。`/voice` を実行し直してください。"
	}
	return b.speaker(guild, speaker, true)
}

func (b *Bot) voiceSelection(e *events.ComponentInteractionCreate) {
	data, ok := e.Data.(discord.StringSelectMenuInteractionData)
	if !ok || !strings.HasPrefix(data.CustomID(), "cloudcord-voice:") {
		return
	}
	if e.GuildID() == nil || e.Member() == nil || len(data.Values) != 1 {
		return
	}
	if err := e.DeferUpdateMessage(); err != nil {
		return
	}
	b.mu.Lock()
	if b.closing || b.ctx.Err() != nil {
		b.mu.Unlock()
		return
	}
	b.commands.Add(1)
	b.mu.Unlock()
	go func() {
		defer b.commands.Done()
		guild := *e.GuildID()
		lock := b.guildLock(guild)
		lock.Lock()
		defer lock.Unlock()
		result := b.selectVoice(guild, e.User().ID, data.CustomID(), data.Values[0])
		ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
		defer cancel()
		update := discord.MessageUpdate{}.WithContent(result).WithComponents(b.voiceMenus(guild)...)
		if _, err := b.Client.Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), update, rest.WithCtx(ctx)); err != nil {
			slog.Warn("interaction_response_failed")
		}
	}()
}
