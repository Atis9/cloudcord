package discordbot

import (
	"context"
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
	speech     *session.GuildSession
	conn       voice.Conn
	recovering bool
	emptyTimer *time.Timer
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

var speakerChoices = []discord.ApplicationCommandOptionChoiceString{
	{Name: "Sulafat", Value: "Sulafat"},
	{Name: "Vindemiatrix", Value: "Vindemiatrix"},
	{Name: "Kore", Value: "Kore"},
}

func New(ctx context.Context, token string, p tts.Provider, c audio.Converter) (*Bot, error) {
	ctx, cancel := context.WithCancel(ctx)
	b := &Bot{ctx: ctx, cancel: cancel, tts: p, converter: c, sessions: map[snowflake.ID]*active{}, locks: map[snowflake.ID]*sync.Mutex{}}
	identify := &identifyGuard{ctx: ctx, IdentifyRateLimiter: gateway.NewIdentifyRateLimiter(gateway.WithIdentifyWait(2 * time.Minute))}
	client, err := disgo.New(token,
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(gateway.IntentGuilds, gateway.IntentGuildMessages, gateway.IntentGuildVoiceStates, gateway.IntentMessageContent),
			gateway.WithAutoReconnect(true),
			gateway.WithIdentifyRateLimiter(identify),
		),
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds, cache.FlagChannels, cache.FlagRoles, cache.FlagVoiceStates)),
		bot.WithVoiceManagerConfigOpts(voice.WithDaveSessionCreateFunc(golibdave.NewSession), voice.WithConnCreateFunc(b.createVoiceConn)),
		bot.WithEventListenerFunc(b.command), bot.WithEventListenerFunc(b.message), bot.WithEventListenerFunc(b.voiceState),
	)
	if err != nil {
		cancel()
		return nil, err
	}
	b.Client = client
	identify.getGatewayBot = client.Rest.GetGatewayBot
	return b, nil
}
func commandDefinitions() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{Name: "join", Description: "参加中のボイスチャンネルで読み上げを開始", DefaultMemberPermissions: omit.NewNilPtr[discord.Permissions](), Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild}, Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionChannel{Name: "text_channel", Description: "読み上げ対象（省略時は現在のテキストチャンネル）", ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildText}}}},
		discord.SlashCommandCreate{Name: "leave", Description: "読み上げを停止して退出", DefaultMemberPermissions: omit.NewNilPtr[discord.Permissions](), Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild}},
		discord.SlashCommandCreate{
			Name: "voice", Description: "読み上げの話者を確認・変更",
			DefaultMemberPermissions: omit.NewNilPtr[discord.Permissions](),
			Contexts:                 []discord.InteractionContextType{discord.InteractionContextTypeGuild},
			Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
				Name: "speaker", Description: "話者（省略すると現在の設定を表示）", Choices: speakerChoices,
			}},
		},
	}
}

func (b *Bot) Register(app snowflake.ID) error {
	// Upsert our commands without deleting unrelated commands.
	for _, cmd := range commandDefinitions() {
		if _, err := b.Client.Rest.CreateGlobalCommand(app, cmd, rest.WithCtx(b.ctx)); err != nil {
			return err
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
	name := data.CommandName()
	if name != "join" && name != "leave" && name != "voice" {
		return
	}
	if b.ctx.Err() != nil {
		return
	}
	if e.GuildID() == nil || e.Member() == nil {
		_ = e.CreateMessage(discord.MessageCreate{Content: "サーバー内で実行してください。", Flags: discord.MessageFlagEphemeral})
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
		} else if reason := b.controlError(*e.GuildID(), e.User().ID); reason != "" {
			result = reason
		} else {
			switch name {
			case "join":
				result = b.join(e)
			case "leave":
				b.leave(*e.GuildID())
			case "voice":
				speaker, present := data.OptString("speaker")
				result = b.speaker(*e.GuildID(), speaker, present)
			}
		}
		reply, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := b.Client.Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), discord.MessageUpdate{}.WithContent(result), rest.WithCtx(reply)); err != nil {
			slog.Warn("interaction_response_failed")
		}
	}()
}

// Caller holds the guild command lock.
func (b *Bot) controlError(guild, user snowflake.ID) string {
	state, ok := b.Client.Caches.VoiceState(guild, user)
	if !ok || state.ChannelID == nil {
		return "通常のボイスチャンネルに参加してから実行してください。"
	}
	b.mu.Lock()
	current := b.sessions[guild]
	b.mu.Unlock()
	if current != nil && current.speech.VoiceChannelID != state.ChannelID.String() {
		return "Bot と同じボイスチャンネルに参加してから実行してください。"
	}
	return ""
}

// Caller holds the guild command lock and has passed controlError.
func (b *Bot) speaker(guild snowflake.ID, speaker string, present bool) string {
	b.mu.Lock()
	current := b.sessions[guild]
	b.mu.Unlock()
	if current == nil {
		return "`/join` で読み上げを開始してから実行してください。"
	}
	if !present {
		if selected := current.speech.Speaker(); selected != "" {
			return fmt.Sprintf("現在の話者は **%s** です。", selected)
		}
		return "現在は既定の話者を使っています。"
	}
	for _, choice := range speakerChoices {
		if speaker == choice.Value {
			current.speech.SetSpeaker(speaker)
			return fmt.Sprintf("これから投稿されるメッセージを **%s** で読み上げます。", speaker)
		}
	}
	return "一覧から話者を選んでください。"
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
	member := e.Member().Member
	member.GuildID = guild
	member.User = e.User()
	if !b.Client.Caches.MemberPermissionsInChannel(tc, member).Has(discord.PermissionViewChannel) {
		return "閲覧権限のあるテキストチャンネルを指定してください。"
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
	if current != nil {
		a.speech.SetSpeaker(current.speech.Speaker())
	}
	b.mu.Lock()
	b.sessions[guild] = a
	b.updateEmptyTimer(guild, a)
	b.mu.Unlock()
	slog.Info("voice_joined", "guild_id", guild.String())
	return fmt.Sprintf("<#%s> の投稿を読み上げます。", textID)
}

// Caller holds the guild command lock.
func (b *Bot) leave(guild snowflake.ID) {
	b.mu.Lock()
	a := b.sessions[guild]
	delete(b.sessions, guild)
	if a != nil {
		a.cancelEmptyTimer()
	}
	b.mu.Unlock()
	if a != nil {
		closeVoiceSession(guild, a)
	}
}

func closeVoiceSession(guild snowflake.ID, a *active) {
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
	state := e.VoiceState
	guild := state.GuildID
	b.mu.Lock()
	a := b.sessions[guild]
	if a == nil || a.recovering {
		b.mu.Unlock()
		return
	}
	channel := snowflake.MustParse(a.speech.VoiceChannelID)
	inChannel := state.ChannelID != nil && *state.ChannelID == channel
	if state.UserID != b.Client.ID() {
		if old := e.OldVoiceState.ChannelID; inChannel || (old != nil && *old == channel) {
			b.updateEmptyTimer(guild, a)
		}
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	if inChannel {
		return
	}
	// Never block Gateway dispatch while stopping a voice connection.
	go func() {
		lock := b.guildLock(guild)
		lock.Lock()
		defer lock.Unlock()
		b.mu.Lock()
		same := b.sessions[guild] == a && !a.recovering
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
