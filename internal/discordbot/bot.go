package discordbot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"cloudcord/internal/audio"
	"cloudcord/internal/session"
	"cloudcord/internal/tts"
	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave/golibdave"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
)

type active struct {
	speech *session.GuildSession
	conn   voice.Conn
}
type Bot struct {
	Client    *bot.Client
	ctx       context.Context
	cancel    context.CancelFunc
	closing   bool
	tts       tts.Provider
	converter audio.Converter
	mu        sync.Mutex
	sessions  map[snowflake.ID]*active
	locks     map[snowflake.ID]*sync.Mutex
	commands  sync.WaitGroup
}

func New(ctx context.Context, token string, p tts.Provider, c audio.Converter) (*Bot, error) {
	ctx, cancel := context.WithCancel(ctx)
	b := &Bot{ctx: ctx, cancel: cancel, tts: p, converter: c, sessions: map[snowflake.ID]*active{}, locks: map[snowflake.ID]*sync.Mutex{}}
	client, err := disgo.New(token,
		bot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentGuilds, gateway.IntentGuildMessages, gateway.IntentGuildVoiceStates, gateway.IntentMessageContent), gateway.WithAutoReconnect(true)),
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds, cache.FlagChannels, cache.FlagRoles, cache.FlagVoiceStates)),
		bot.WithVoiceManagerConfigOpts(voice.WithDaveSessionCreateFunc(golibdave.NewSession)),
		bot.WithEventListenerFunc(b.command), bot.WithEventListenerFunc(b.message), bot.WithEventListenerFunc(b.voiceState),
	)
	if err != nil {
		cancel()
		return nil, err
	}
	b.Client = client
	return b, nil
}
func (b *Bot) Register(app snowflake.ID) error {
	permission := discord.PermissionManageGuild
	cmds := []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{Name: "join", Description: "参加中のボイスチャンネルで読み上げを開始", DefaultMemberPermissions: omit.New(&permission), Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild}, Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionChannel{Name: "text_channel", Description: "読み上げ対象（省略時は現在のテキストチャンネル）", ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildText}}}},
		discord.SlashCommandCreate{Name: "leave", Description: "読み上げを停止して退出", DefaultMemberPermissions: omit.New(&permission), Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild}},
	}
	// Upsert these two commands without deleting unrelated commands.
	for _, cmd := range cmds {
		if _, err := b.Client.Rest.CreateGlobalCommand(app, cmd, rest.WithCtx(b.ctx)); err != nil {
			return errors.New("slash command registration failed")
		}
	}
	return nil
}
func (b *Bot) guildLock(id snowflake.ID) *sync.Mutex {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.locks[id] == nil {
		b.locks[id] = &sync.Mutex{}
	}
	return b.locks[id]
}
func (b *Bot) command(e *events.ApplicationCommandInteractionCreate) {
	data, ok := e.Data.(discord.SlashCommandInteractionData)
	if !ok {
		return
	}
	if data.CommandName() != "join" && data.CommandName() != "leave" {
		return
	}
	if b.ctx.Err() != nil {
		return
	}
	if e.GuildID() == nil || e.Member() == nil || !(e.Member().Permissions.Has(discord.PermissionManageGuild) || e.Member().Permissions.Has(discord.PermissionAdministrator)) {
		_ = e.CreateMessage(discord.MessageCreate{Content: "サーバー管理権限が必要です。", Flags: discord.MessageFlagEphemeral})
		return
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return
	}
	b.mu.Lock()
	if b.closing {
		b.mu.Unlock()
		return
	}
	b.commands.Add(1)
	b.mu.Unlock()
	go func() {
		defer b.commands.Done()
		lock := b.guildLock(*e.GuildID())
		lock.Lock()
		defer lock.Unlock()
		result := "読み上げを停止しました。"
		if b.ctx.Err() != nil {
			result = "Bot は停止中です。"
		} else if data.CommandName() == "leave" {
			b.leave(*e.GuildID())
		} else {
			result = b.join(e)
		}
		reply, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := b.Client.Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), discord.MessageUpdate{}.WithContent(result), rest.WithCtx(reply)); err != nil {
			slog.Warn("interaction_response_failed")
		}
	}()
}
func (b *Bot) join(e *events.ApplicationCommandInteractionCreate) string {
	guild := *e.GuildID()
	state, ok := b.Client.Caches.VoiceState(guild, e.User().ID)
	if !ok || state.ChannelID == nil {
		return "通常のボイスチャンネルに参加してから実行してください。"
	}
	vc, ok := b.Client.Caches.Channel(*state.ChannelID)
	if !ok || vc.Type() != discord.ChannelTypeGuildVoice {
		return "通常のボイスチャンネルのみ対応しています。"
	}
	textID := e.Channel().ID()
	if id, present := e.SlashCommandInteractionData().OptSnowflake("text_channel"); present {
		textID = id
	}
	tc, ok := b.Client.Caches.Channel(textID)
	if !ok || tc.GuildID() != guild || tc.Type() != discord.ChannelTypeGuildText {
		return "同じサーバーの通常のテキストチャンネルを指定してください。"
	}
	ctx, cancel := context.WithTimeout(b.ctx, 15*time.Second)
	defer cancel()
	self, err := b.Client.Rest.GetMember(guild, b.Client.ID(), rest.WithCtx(ctx))
	if err != nil {
		return "Bot の権限を確認できませんでした。"
	}
	if !b.Client.Caches.MemberPermissionsInChannel(vc, *self).Has(discord.PermissionViewChannel, discord.PermissionConnect, discord.PermissionSpeak) {
		return "Bot にボイスチャンネルの View Channel / Connect / Speak 権限が必要です。"
	}
	if !b.Client.Caches.MemberPermissionsInChannel(tc, *self).Has(discord.PermissionViewChannel) {
		return "Bot に対象テキストチャンネルの View Channel 権限が必要です。"
	}
	b.mu.Lock()
	current := b.sessions[guild]
	b.mu.Unlock()
	if current != nil && current.speech.VoiceChannelID == state.ChannelID.String() && current.speech.TextChannelID == textID.String() {
		return "このチャンネルでは既に読み上げ中です。"
	}
	b.leave(guild)
	conn := b.Client.VoiceManager.CreateConn(guild)
	if err = conn.Open(ctx, *state.ChannelID, false, true); err != nil {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn.Close(closeCtx)
		closeCancel()
		slog.Warn("voice_join_failed", "guild_id", guild.String())
		return "ボイス接続に失敗しました。権限とネットワークを確認してください。"
	}
	if b.ctx.Err() != nil {
		conn.Close(ctx)
		return "Bot は停止中です。"
	}
	a := &active{conn: conn, speech: session.New(b.ctx, guild.String(), textID.String(), state.ChannelID.String(), b.tts, b.converter, voicePlayer{conn})}
	b.mu.Lock()
	b.sessions[guild] = a
	b.mu.Unlock()
	slog.Info("voice_joined", "guild_id", guild.String())
	return fmt.Sprintf("<#%s> の投稿を読み上げます。", textID)
}

// Caller holds the guild command lock.
func (b *Bot) leave(guild snowflake.ID) {
	b.mu.Lock()
	a := b.sessions[guild]
	delete(b.sessions, guild)
	b.mu.Unlock()
	if a == nil {
		return
	}
	a.speech.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.conn.Close(ctx)
	slog.Info("voice_left", "guild_id", guild.String())
}
func (b *Bot) message(e *events.MessageCreate) {
	m := e.Message
	if e.GuildID == nil || m.Author.Bot || m.Author.ID == b.Client.ID() || m.WebhookID != nil || m.Content == "" || m.Type != discord.MessageTypeDefault {
		return
	}
	b.mu.Lock()
	a := b.sessions[*e.GuildID]
	b.mu.Unlock()
	if a == nil || a.speech.TextChannelID != e.ChannelID.String() {
		return
	}
	names := map[string]string{}
	for _, user := range m.Mentions {
		name := user.Username
		if user.GlobalName != nil {
			name = *user.GlobalName
		}
		names[user.ID.String()] = name
	}
	a.speech.Enqueue(Normalize(m.Content, names))
}
func (b *Bot) voiceState(e *events.GuildVoiceStateUpdate) {
	if e.VoiceState.UserID != b.Client.ID() {
		return
	}
	guild := e.VoiceState.GuildID
	b.mu.Lock()
	a := b.sessions[guild]
	b.mu.Unlock()
	if a == nil || (e.VoiceState.ChannelID != nil && e.VoiceState.ChannelID.String() == a.speech.VoiceChannelID) {
		return
	}
	// Never block Gateway dispatch while stopping a voice connection.
	go func() {
		lock := b.guildLock(guild)
		lock.Lock()
		defer lock.Unlock()
		b.mu.Lock()
		same := b.sessions[guild] == a
		b.mu.Unlock()
		if same {
			b.leave(guild)
		}
	}()
}
func (b *Bot) Close() {
	b.mu.Lock()
	b.closing = true
	b.cancel()
	b.mu.Unlock()
	b.commands.Wait()
	b.mu.Lock()
	ids := make([]snowflake.ID, 0, len(b.sessions))
	for id := range b.sessions {
		ids = append(ids, id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		lock := b.guildLock(id)
		lock.Lock()
		b.leave(id)
		lock.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b.Client.Close(ctx)
}
