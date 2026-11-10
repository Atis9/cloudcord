package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/rest"
)

func TestRedactGatewayPayload(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&log, &slog.HandlerOptions{ReplaceAttr: redactGatewayPayload}))
	logger.Error("error while parsing gateway message", "err", errors.New("failed to unmarshal message data: private payload"))
	if strings.Contains(log.String(), "private payload") || !strings.Contains(log.String(), "Discord Gateway payload could not be decoded") {
		t.Fatal("Gateway payload should be omitted from logs")
	}
}

func TestStartupError(t *testing.T) {
	t.Setenv("DISCORD_TOKEN", "private-token")
	t.Setenv("CLOUDFLARE_BOT_API_TOKEN", "private-api-token")
	err := startupError("initialization", errors.New("dial: private-token private-api-token timeout"))
	if strings.Contains(err.Error(), "private-") || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("unsafe or unhelpful error: %s", err)
	}
	upstream := &rest.Error{Response: &http.Response{StatusCode: 403}, RsBody: []byte("sensitive response"), Code: 50001}
	err = startupError("initialization", fmt.Errorf("wrapped: %w", upstream))
	if err.Error() != "initialization: Discord HTTP 403 (code 50001)" {
		t.Fatal(err)
	}
}
