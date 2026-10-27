package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"cloudcord/internal/audio"
	"cloudcord/internal/discordbot"
	"cloudcord/internal/tts"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("startup_failed", "error", err)
		os.Exit(1)
	}
}

const readinessPath = "/tmp/cloudcord-ready"

func run() error {
	if err := os.Remove(readinessPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear readiness: %w", err)
	}
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
		return startupError("Discord client initialization failed", err)
	}
	defer b.Close()
	if err = b.Register(app); err != nil {
		return startupError("Discord command registration failed", err)
	}
	if err = b.Client.OpenGateway(ctx); err != nil {
		return startupError("Discord Gateway connection failed", err)
	}
	if err = os.WriteFile(readinessPath, []byte("ready\n"), 0600); err != nil {
		return fmt.Errorf("write readiness: %w", err)
	}
	defer os.Remove(readinessPath)
	slog.Info("bot_running")
	<-ctx.Done()
	return nil
}

// Keep useful startup failures without logging HTTP bodies or credentials.
func startupError(stage string, err error) error {
	var apiError *rest.Error
	if errors.As(err, &apiError) {
		status := 0
		if apiError.Response != nil {
			status = apiError.Response.StatusCode
		}
		return fmt.Errorf("%s: Discord HTTP %d (code %d)", stage, status, apiError.Code)
	}
	message := err.Error()
	for _, name := range []string{"DISCORD_TOKEN", "CLOUDFLARE_BOT_API_TOKEN"} {
		if value := os.Getenv(name); value != "" {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	return fmt.Errorf("%s: %s", stage, message)
}
