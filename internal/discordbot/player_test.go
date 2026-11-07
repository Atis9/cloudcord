package discordbot

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/disgoorg/disgo/voice"
)

type testUDP struct {
	voice.UDPConn
	packets [][]byte
	failure error
}

func (u *testUDP) SetWriteDeadline(time.Time) error { return nil }
func (u *testUDP) Write(p []byte) (int, error) {
	if u.failure != nil {
		return 0, u.failure
	}
	u.packets = append(u.packets, bytes.Clone(p))
	return len(p), nil
}

type testVoice struct {
	voice.Conn
	udp   *testUDP
	flags []voice.SpeakingFlags
}

func (c *testVoice) UDP() voice.UDPConn { return c.udp }
func (c *testVoice) SetSpeaking(ctx context.Context, f voice.SpeakingFlags) error {
	c.flags = append(c.flags, f)
	return nil
}
func TestPlayerPacingAndSpeaking(t *testing.T) {
	c := &testVoice{udp: &testUDP{}}
	frames := [][]byte{{0xfc, 1}, {0xfc, 2}}
	start := time.Now()
	startedAt := 0
	if e := (voicePlayer{c}).Play(context.Background(), &testFrames{data: frames}, func() { startedAt = len(c.udp.packets) }); e != nil {
		t.Fatal(e)
	}
	if time.Since(start) < 160*time.Millisecond {
		t.Fatal("packets were not paced")
	}
	if len(c.udp.packets) != 9 || startedAt != 3 || !bytes.Equal(c.udp.packets[2], frames[0]) || !bytes.Equal(c.udp.packets[3], frames[1]) {
		t.Fatal("bad packets")
	}
	for _, p := range append(c.udp.packets[:2:2], c.udp.packets[4:]...) {
		if !bytes.Equal(p, voice.SilenceAudioFrame) {
			t.Fatal("missing silence at playback boundary")
		}
	}
	if len(c.flags) != 2 || c.flags[0] != voice.SpeakingFlagMicrophone || c.flags[1] != voice.SpeakingFlagNone {
		t.Fatal("speaking state not reset")
	}
}
func TestPlayerCancelAndWriteFailure(t *testing.T) {
	c := &testVoice{udp: &testUDP{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := (voicePlayer{c}).Play(ctx, &testFrames{data: [][]byte{{0xfc}}}, nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if len(c.udp.packets) != 0 || len(c.flags) != 0 {
		t.Fatal("cancelled player sent audio")
	}
	c = &testVoice{udp: &testUDP{failure: errors.New("UDP failure")}}
	if e := (voicePlayer{c}).Play(context.Background(), &testFrames{data: [][]byte{{0xfc}}}, nil); e == nil {
		t.Fatal("write failure ignored")
	}
	if c.flags[len(c.flags)-1] != voice.SpeakingFlagNone {
		t.Fatal("speaking flag remains active")
	}
}

type testFrames struct{ data [][]byte }

func (f *testFrames) Next(ctx context.Context) ([]byte, error) {
	if len(f.data) == 0 {
		return nil, io.EOF
	}
	p := f.data[0]
	f.data = f.data[1:]
	return p, nil
}
func (f *testFrames) Close() error { return nil }
