package discordbot

import (
	"github.com/disgoorg/godave/libdave"
	"testing"
)

func TestNativeDAVELink(t *testing.T) {
	if libdave.MaxSupportedProtocolVersion() < 1 {
		t.Fatal("native libdave has no DAVE protocol support")
	}
}
