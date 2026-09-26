package scenes

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// WavFormat is the PCM format of a WAV file.
type WavFormat struct {
	Channels      int
	SampleRate    int
	BitsPerSample int
	DataBytes     int
}

// DurationMs is the length of the PCM data.
func (f WavFormat) DurationMs() int {
	bytesPerSec := f.SampleRate * f.Channels * f.BitsPerSample / 8
	if bytesPerSec <= 0 {
		return 0
	}
	return int(int64(f.DataBytes) * 1000 / int64(bytesPerSec))
}

var errNotWav = errors.New("scenes: not a PCM WAV file")

// ParseWav reads the fmt and data chunk sizes of a RIFF/WAVE file.
func ParseWav(b []byte) (WavFormat, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return WavFormat{}, errNotWav
	}
	var f WavFormat
	pos := 12
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		body := pos + 8
		switch id {
		case "fmt ":
			if body+16 > len(b) {
				return WavFormat{}, errNotWav
			}
			f.Channels = int(binary.LittleEndian.Uint16(b[body+2:]))
			f.SampleRate = int(binary.LittleEndian.Uint32(b[body+4:]))
			f.BitsPerSample = int(binary.LittleEndian.Uint16(b[body+14:]))
		case "data":
			f.DataBytes = min(size, len(b)-body)
			if f.SampleRate == 0 {
				return WavFormat{}, errNotWav
			}
			return f, nil
		}
		pos = body + size + size%2
	}
	return WavFormat{}, errNotWav
}

// SilenceWav is a PCM WAV of ms milliseconds of silence in format f.
func SilenceWav(f WavFormat, ms int) []byte {
	frame := f.Channels * f.BitsPerSample / 8
	n := f.SampleRate * ms / 1000 * frame
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+n))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(f.Channels))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(f.SampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(f.SampleRate*frame))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(frame))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(f.BitsPerSample))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(n))
	buf.Write(make([]byte, n))
	return buf.Bytes()
}
