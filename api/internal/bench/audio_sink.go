package bench

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// maxSinkObjectBytes bounds one stored object (the longest benchmark
// audio is a few minutes of 48 kHz mono PCM).
const maxSinkObjectBytes = 512 << 20

// Sink is a small in-memory object store the benchmark serves over HTTP
// so the Python worker can PUT synthesized audio and GET inputs through
// the same presigned-URL contract it uses with MinIO in production,
// without the benchmark needing storage credentials. URLs carry a random
// token; the server listens only while the benchmark runs.
type Sink struct {
	mu      sync.Mutex
	objects map[string][]byte
	token   string
	base    string
	server  *http.Server
	lis     net.Listener
}

// NewSink starts a sink reachable at advertiseHost (see AdvertiseHost).
func NewSink(advertiseHost string) (*Sink, error) {
	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, err
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		_ = lis.Close()
		return nil, err
	}
	s := &Sink{objects: map[string][]byte{}, token: hex.EncodeToString(raw[:]), lis: lis}
	port := lis.Addr().(*net.TCPAddr).Port
	s.base = fmt.Sprintf("http://%s/%s/", net.JoinHostPort(advertiseHost, fmt.Sprint(port)), s.token)
	s.server = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.server.Serve(lis) }()
	return s, nil
}

// AdvertiseHost returns the local address this process uses to reach
// target (host:port), i.e. the address the target can reach it on. No
// packet is sent: connecting a UDP socket only selects the route.
func AdvertiseHost(target string) (string, error) {
	conn, err := net.Dial("udp", target)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

// Close stops the server.
func (s *Sink) Close() error { return s.server.Close() }

// URL is the address of object name (PUT to store, GET to read).
func (s *Sink) URL(name string) string { return s.base + name }

// Put stores data under name directly.
func (s *Sink) Put(name string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[name] = data
}

// Get returns the object stored under name.
func (s *Sink) Get(name string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[name]
	return data, ok
}

func (s *Sink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutPrefix(r.URL.Path, "/"+s.token+"/")
	if !ok || name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut:
		data, err := io.ReadAll(io.LimitReader(r.Body, maxSinkObjectBytes+1))
		if err != nil || len(data) > maxSinkObjectBytes {
			http.Error(w, "object too large or unreadable", http.StatusRequestEntityTooLarge)
			return
		}
		s.Put(name, data)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		data, ok := s.Get(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		_, _ = w.Write(data)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// WAV is 16-bit PCM mono audio, the format the Python worker returns.
type WAV struct {
	SampleRate int
	PCM        []byte // little-endian int16 samples
}

// Seconds is the audio's length.
func (w WAV) Seconds() float64 {
	if w.SampleRate == 0 {
		return 0
	}
	return float64(len(w.PCM)/2) / float64(w.SampleRate)
}

// ParseWAV reads a 16-bit PCM mono RIFF/WAVE file.
func ParseWAV(data []byte) (WAV, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return WAV{}, errors.New("bench: not a RIFF/WAVE file")
	}
	var out WAV
	var gotFmt bool
	for off := 12; off+8 <= len(data); {
		id := string(data[off : off+4])
		size := int(le32(data[off+4:]))
		body := off + 8
		if size < 0 || body+size > len(data) {
			return WAV{}, fmt.Errorf("bench: truncated %q chunk", id)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return WAV{}, errors.New("bench: short fmt chunk")
			}
			format, channels, bits := le16(data[body:]), le16(data[body+2:]), le16(data[body+14:])
			if format != 1 || channels != 1 || bits != 16 {
				return WAV{}, fmt.Errorf("bench: want 16-bit PCM mono, got format %d, %d channels, %d bits", format, channels, bits)
			}
			out.SampleRate = int(le32(data[body+4:]))
			gotFmt = true
		case "data":
			if !gotFmt {
				return WAV{}, errors.New("bench: data chunk before fmt chunk")
			}
			out.PCM = data[body : body+size]
			return out, nil
		}
		off = body + size + size%2
	}
	return WAV{}, errors.New("bench: no data chunk")
}

// Bytes encodes w as a canonical 44-byte-header WAV file.
func (w WAV) Bytes() []byte {
	n := len(w.PCM)
	out := make([]byte, 44, 44+n)
	copy(out[0:], "RIFF")
	put32(out[4:], uint32(36+n)) //nolint:gosec // bounded by maxSinkObjectBytes
	copy(out[8:], "WAVEfmt ")
	put32(out[16:], 16)
	put16(out[20:], 1)
	put16(out[22:], 1)
	put32(out[24:], uint32(w.SampleRate))   //nolint:gosec // an audio sample rate
	put32(out[28:], uint32(w.SampleRate*2)) //nolint:gosec // an audio byte rate
	put16(out[32:], 2)
	put16(out[34:], 16)
	copy(out[36:], "data")
	put32(out[40:], uint32(n)) //nolint:gosec // bounded by maxSinkObjectBytes
	return append(out, w.PCM...)
}

// ConcatWAV joins clips of one sample rate with gapS of silence between
// them and returns the start offset (seconds) of every clip.
func ConcatWAV(clips []WAV, gapS float64) (WAV, []float64, error) {
	if len(clips) == 0 {
		return WAV{}, nil, errors.New("bench: nothing to concatenate")
	}
	rate := clips[0].SampleRate
	gap := make([]byte, 2*int(gapS*float64(rate)))
	out := WAV{SampleRate: rate}
	starts := make([]float64, 0, len(clips))
	for i, c := range clips {
		if c.SampleRate != rate {
			return WAV{}, nil, fmt.Errorf("bench: clip %d is %d Hz, want %d Hz", i, c.SampleRate, rate)
		}
		if i > 0 {
			out.PCM = append(out.PCM, gap...)
		}
		starts = append(starts, out.Seconds())
		out.PCM = append(out.PCM, c.PCM...)
	}
	return out, starts, nil
}

func le16(b []byte) int { return int(b[0]) | int(b[1])<<8 }
func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
func put16(b []byte, v uint16) {
	b[0], b[1] = byte(v), byte(v>>8)
}

func put32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
