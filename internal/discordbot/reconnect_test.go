package discordbot

import (
	"context"
	"errors"
	"sync"
	"testing"

	"cloudcord/internal/session"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/gorilla/websocket"
)

type recoveryGateway struct{ voice.Gateway }
type recoveryConn struct {
	voice.Conn
	gateway voice.Gateway
	openErr error
	channel snowflake.ID
	closed  bool
}

func (c *recoveryConn) Gateway() voice.Gateway { return c.gateway }
func (c *recoveryConn) Open(_ context.Context, channel snowflake.ID, mute, deaf bool) error {
	c.channel = channel
	if mute || !deaf {
		panic("unexpected voice flags")
	}
	return c.openErr
}
func (c *recoveryConn) Close(context.Context) { c.closed = true }

type recoveryManager struct {
	voice.Manager
	conn    *recoveryConn
	cleaned *bool
	t       *testing.T
	created bool
}

func (m *recoveryManager) CreateConn(snowflake.ID) voice.Conn {
	if !*m.cleaned {
		m.t.Fatal("created connection before old cleanup")
	}
	m.created = true
	return m.conn
}

func TestExpiredVoiceSession(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		code                 int
		fail, stale, closing bool
		want                 bool
	}{
		{name: "expired", code: 4006, want: true},
		{name: "timeout", code: 4009, want: true},
		{name: "failed rejoin clears session", code: 4006, fail: true, want: true},
		{name: "kicked", code: 4014},
		{name: "old callback", code: 4006, stale: true},
		{name: "shutdown", code: 4006, closing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			gateway := &recoveryGateway{}
			old := &active{conn: &recoveryConn{gateway: gateway}, speech: session.New(ctx, "1", "2", "3", nil, nil, nil)}
			defer old.speech.Stop()
			cleaned := false
			next := &recoveryConn{}
			if tc.fail {
				next.openErr = errors.New("offline")
			}
			manager := &recoveryManager{conn: next, cleaned: &cleaned, t: t}
			b := &Bot{ctx: ctx, closing: tc.closing, Client: &bot.Client{VoiceManager: manager}, sessions: map[snowflake.ID]*active{1: old}, locks: map[snowflake.ID]*sync.Mutex{}}
			var callback voice.Gateway = gateway
			if tc.stale {
				callback = &recoveryGateway{}
			}
			b.voiceClosed(1, callback, &websocket.CloseError{Code: tc.code}, false, func(voice.Gateway, error, bool) { cleaned = true })
			if !cleaned || manager.created != tc.want {
				t.Fatalf("cleaned=%v created=%v", cleaned, manager.created)
			}
			current := b.sessions[1]
			if !tc.want {
				if current != old {
					t.Fatal("changed unrelated session")
				}
				return
			}
			if next.channel != 3 {
				t.Fatal("wrong voice channel")
			}
			if tc.fail {
				if current != nil || !next.closed {
					t.Fatal("failed connection not cleaned")
				}
				return
			}
			if current == nil || current == old || current.speech.TextChannelID != "2" {
				t.Fatal("session not replaced")
			}
			current.speech.Stop()
		})
	}
}
