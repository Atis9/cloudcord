package audio

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"

	"cloudcord/internal/tts"
)

type FrameStream interface {
	Next(context.Context) ([]byte, error)
	Close() error
}
type Converter interface {
	Convert(context.Context, tts.AudioResult) (FrameStream, error)
}
type FFmpeg struct {
	Path  string
	slots chan struct{}
}

// Short normalization windows keep streaming startup bounded. Limit gain to 20 dB,
// avoid boosting breaths between phrases, and disable the limiter's makeup gain.
const speechFilter = "atempo=1.2," +
	"dynaudnorm=framelen=50:gausssize=5:peak=0.9:maxgain=10:targetrms=0.15:altboundary=1:threshold=0.015," +
	"acompressor=threshold=0.25:ratio=3:attack=5:release=100," +
	"alimiter=limit=0.89:level=0:latency=1"

func NewFFmpeg(path string) *FFmpeg { return &FFmpeg{Path: path, slots: make(chan struct{}, 1)} }
func (f *FFmpeg) Convert(ctx context.Context, input tts.AudioResult) (FrameStream, error) {
	format := "mp3"
	switch input.MIME {
	case "audio/mpeg":
	case "audio/wav":
		format = "wav"
	default:
		input.Stream.Close()
		return nil, errors.New("unsupported audio MIME")
	}
	select {
	case f.slots <- struct{}{}:
	case <-ctx.Done():
		input.Stream.Close()
		return nil, ctx.Err()
	}
	child, cancel := context.WithCancel(ctx)
	stream := &ffmpegStream{cancel: cancel, input: input.Stream, frames: make(chan []byte, 3001), done: make(chan struct{})}
	context.AfterFunc(child, stream.closeInput)
	var source io.Reader = tts.LimitAudio(input.Stream)
	inputArgs := []string{"-f", format, "-probesize", "4096", "-analyzeduration", "0"}
	if format == "wav" {
		var pcm bool
		source, pcm = geminiPCM(source)
		if pcm {
			inputArgs = []string{"-f", "s16le", "-ar", "24000", "-ac", "1", "-probesize", "4096", "-analyzeduration", "1"}
		}
	}
	if err := child.Err(); err != nil {
		cancel()
		stream.closeInput()
		<-f.slots
		return nil, err
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-filter_threads", "1"}
	args = append(args, inputArgs...)
	// One Opus packet per Ogg page + flush prevents the default one-second page wait.
	args = append(args, "-i", "pipe:0", "-map", "0:a:0", "-vn", "-t", "60", "-af", speechFilter, "-ar", "48000", "-ac", "2", "-c:a", "libopus", "-b:a", "64k", "-vbr", "off", "-frame_duration", "20", "-application", "audio", "-threads", "1", "-f", "ogg", "-page_duration", "1", "-flush_packets", "1", "pipe:1")
	cmd := exec.CommandContext(child, f.Path, args...)
	cmd.Stdin = source
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		stream.closeInput()
		<-f.slots
		return nil, errors.New("FFmpeg pipe failed")
	}
	if err = cmd.Start(); err != nil {
		cancel()
		stdout.Close()
		stream.closeInput()
		<-f.slots
		return nil, errors.New("FFmpeg start failed")
	}
	stream.output = stdout
	// Bound eager decoding to one utterance (60s). It can finish and free the
	// process slot while Discord consumes the already available frames in real time.
	go func() {
		parseErr := walkOpus(io.LimitReader(stdout, (8<<20)+1), func(frame []byte) error {
			select {
			case stream.frames <- frame:
				return nil
			case <-child.Done():
				return child.Err()
			}
		})
		if parseErr != nil {
			cancel()
		}
		stream.closeInput()
		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			stream.err = ctx.Err()
		} else if parseErr != nil {
			stream.err = parseErr
		} else if waitErr != nil {
			stream.err = errors.New("FFmpeg conversion failed")
		}
		stdout.Close()
		cancel()
		<-f.slots
		close(stream.frames)
		close(stream.done)
	}()
	return stream, nil
}

type ffmpegStream struct {
	cancel    context.CancelFunc
	input     io.ReadCloser
	output    io.ReadCloser
	inputOnce sync.Once
	frames    chan []byte
	done      chan struct{}
	err       error
}

func (s *ffmpegStream) closeInput() { s.inputOnce.Do(func() { s.input.Close() }) }
func (s *ffmpegStream) Next(ctx context.Context) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case frame, ok := <-s.frames:
		if !ok {
			if s.err != nil {
				return nil, s.err
			}
			return nil, io.EOF
		}
		return frame, nil
	}
}
func (s *ffmpegStream) Close() error {
	s.cancel()
	s.closeInput()
	s.output.Close()
	<-s.done
	return s.err
}
