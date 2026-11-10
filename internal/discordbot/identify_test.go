package discordbot

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
)

func TestGatewayGuildCreateWithUnknownChannelType(t *testing.T) {
	const event = `{"op":0,"t":"GUILD_CREATE","s":4,"d":{"id":"1","channels":[{"id":"2","type":20}]}}`
	var message gateway.Message
	if err := json.Unmarshal([]byte(event), &message); err != nil {
		t.Fatal(err)
	}
	if guild, ok := message.D.(gateway.EventGuildCreate); !ok || len(guild.Channels) != 1 {
		t.Fatal("Discord guild event was not decoded")
	}
}

func TestIdentifyGuardPreservesQuotaAndCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remaining := identifyReserve
		unavailable := false
		g := &identifyGuard{
			IdentifyRateLimiter: gateway.NewIdentifyRateLimiter(gateway.WithIdentifyWait(2 * time.Minute)),
			ctx:                 t.Context(),
			getGatewayBot: func(...rest.RequestOpt) (*discord.GatewayBot, error) {
				if unavailable {
					return nil, errors.New("Discord unavailable")
				}
				return &discord.GatewayBot{SessionStartLimit: discord.SessionStartLimit{Remaining: remaining}}, nil
			},
		}
		if err := g.Wait(t.Context(), 0); err == nil {
			t.Fatal("used the reserved session starts")
		}
		remaining++
		unavailable = true
		start := time.Now()
		if err := g.Wait(t.Context(), 0); err == nil {
			t.Fatal("allowed an Identify without knowing the quota")
		}
		if time.Since(start) < 2*time.Minute {
			t.Fatal("did not wait between attempts")
		}
		unavailable = false
		if err := g.Wait(t.Context(), 0); err != nil {
			t.Fatal(err)
		}
		g.Unlock(0)
	})
}

func TestIdentifyGuardStopsWaitingOnShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		g := &identifyGuard{
			IdentifyRateLimiter: gateway.NewIdentifyRateLimiter(gateway.WithIdentifyWait(2 * time.Minute)),
			ctx:                 ctx,
			getGatewayBot: func(...rest.RequestOpt) (*discord.GatewayBot, error) {
				return &discord.GatewayBot{SessionStartLimit: discord.SessionStartLimit{Remaining: 100}}, nil
			},
		}
		if err := g.Wait(t.Context(), 0); err != nil {
			t.Fatal(err)
		}
		g.Unlock(0)
		result := make(chan error, 1)
		go func() { result <- g.Wait(context.Background(), 0) }()
		synctest.Wait()
		stop()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("expected the pending reconnect to stop, got %v", err)
		}
	})
}
