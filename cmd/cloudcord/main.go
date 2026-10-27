package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"cloudcord/internal/audio"
	"cloudcord/internal/discordbot"
	"cloudcord/internal/tts"
	"github.com/disgoorg/snowflake/v2"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("startup_failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		return errors.New("DISCORD_TOKEN is required")
	}
	app, err := snowflake.Parse(os.Getenv("DISCORD_APPLICATION_ID"))
	if err != nil || app == 0 {
		return errors.New("DISCORD_APPLICATION_ID is required")
	}
	provider, err := tts.NewCloudflare(os.Getenv("CLOUDFLARE_API_BASE_URL"), os.Getenv("CLOUDFLARE_BOT_API_TOKEN"))
	if err != nil {
		return err
	}
	path := os.Getenv("FFMPEG_PATH")
	if path == "" {
		path = "ffmpeg"
	}
	if _, err = exec.LookPath(path); err != nil {
		return errors.New("FFmpeg executable not found")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	b, err := discordbot.New(ctx, token, provider, audio.NewFFmpeg(path))
	if err != nil {
		return errors.New("Discord client initialization failed")
	}
	defer b.Close()
	if err = b.Register(app); err != nil {
		return err
	}
	if err = b.Client.OpenGateway(ctx); err != nil {
		return errors.New("Discord Gateway connection failed")
	}
	slog.Info("bot_running")
	<-ctx.Done()
	return nil
}
