package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// ReadOpus reconstructs laced packets, including packets spanning Ogg pages.
// Only a single stereo Opus logical stream with 20 ms packets is accepted.
func ReadOpus(r io.Reader) ([][]byte, error) {
	var frames [][]byte
	err := walkOpus(r, func(frame []byte) error { frames = append(frames, frame); return nil })
	return frames, err
}

func walkOpus(r io.Reader, emit func([]byte) error) error {
	frames := 0
	var packet []byte
	var serial uint32
	var sequence uint32
	packets := 0
	ended := false
	for {
		header := make([]byte, 27)
		_, err := io.ReadFull(r, header)
		if err == io.EOF {
			if len(packet) != 0 || packets < 3 || !ended {
				return errors.New("incomplete Ogg stream")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if ended || string(header[:4]) != "OggS" || header[4] != 0 {
			return errors.New("invalid Ogg page")
		}
		if sequence == 0 {
			serial = binary.LittleEndian.Uint32(header[14:18])
			if header[5]&2 == 0 {
				return errors.New("missing Ogg BOS")
			}
		}
		if binary.LittleEndian.Uint32(header[14:18]) != serial || binary.LittleEndian.Uint32(header[18:22]) != sequence {
			return errors.New("Ogg stream discontinuity")
		}
		sequence++
		if (header[5]&1 != 0) != (len(packet) > 0) {
			return errors.New("invalid Ogg continuation")
		}
		lacing := make([]byte, int(header[26]))
		if _, err = io.ReadFull(r, lacing); err != nil {
			return err
		}
		size := 0
		for _, n := range lacing {
			size += int(n)
		}
		body := make([]byte, size)
		if _, err = io.ReadFull(r, body); err != nil {
			return err
		}
		expected := binary.LittleEndian.Uint32(header[22:26])
		clear(header[22:26])
		if oggCRC(append(append(header, lacing...), body...)) != expected {
			return errors.New("Ogg checksum mismatch")
		}
		offset := 0
		for _, n := range lacing {
			packet = append(packet, body[offset:offset+int(n)]...)
			offset += int(n)
			if len(packet) > 65536 {
				return errors.New("Ogg packet too large")
			}
			if n == 255 {
				continue
			}
			switch packets {
			case 0:
				if len(packet) < 19 || !bytes.Equal(packet[:8], []byte("OpusHead")) || packet[8] != 1 || packet[9] != 2 || packet[18] != 0 {
					return errors.New("expected stereo Opus header")
				}
			case 1:
				if len(packet) < 8 || string(packet[:8]) != "OpusTags" {
					return errors.New("missing Opus tags")
				}
			default:
				if len(packet) == 0 || len(packet) > 1400 || opusSamples(packet) != 960 {
					return errors.New("expected 20ms Opus packet")
				}
				frames++
				if frames > 3001 {
					return errors.New("audio exceeds 60 seconds")
				}
				if err = emit(bytes.Clone(packet)); err != nil {
					return err
				}
			}
			packets++
			packet = nil
		}
		ended = header[5]&4 != 0
	}
}
func oggCRC(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
func opusSamples(p []byte) int {
	if len(p) == 0 {
		return 0
	}
	config := p[0] >> 3
	var samples int
	switch {
	case config >= 16:
		samples = 120 << (config & 3)
	case config >= 12:
		samples = 480 << (config & 1)
	case config&3 == 3:
		samples = 2880
	default:
		samples = 480 << (config & 3)
	}
	count := 1
	switch p[0] & 3 {
	case 1, 2:
		count = 2
	case 3:
		if len(p) < 2 {
			return 0
		}
		count = int(p[1] & 63)
	}
	return samples * count
}
