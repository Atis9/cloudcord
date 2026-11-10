package discordbot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
)

const identifyReserve = 25

type identifyGuard struct {
	gateway.IdentifyRateLimiter
	ctx           context.Context
	getGatewayBot func(...rest.RequestOpt) (*discord.GatewayBot, error)
}

// DisGo calls Wait before fresh Identify attempts. Resume bypasses this guard.
func (g *identifyGuard) Wait(ctx context.Context, shardID int) error {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(g.ctx, cancel)
	defer stop()
	defer cancel()
	if err := g.ctx.Err(); err != nil {
		return err
	}
	if err := g.IdentifyRateLimiter.Wait(ctx, shardID); err != nil {
		return err
	}

	quotaCtx, cancelQuota := context.WithTimeout(ctx, 10*time.Second)
	defer cancelQuota()
	info, err := g.getGatewayBot(rest.WithCtx(quotaCtx))
	if err != nil || info == nil {
		g.Unlock(shardID)
		return errors.New("could not check Discord Gateway Identify quota")
	}
	remaining := info.SessionStartLimit.Remaining
	if remaining <= identifyReserve {
		g.Unlock(shardID)
		return fmt.Errorf("Discord Gateway Identify held: %d session starts remaining", remaining)
	}
	slog.Info("discord_gateway_identify_permitted", "remaining", remaining)
	return nil
}
