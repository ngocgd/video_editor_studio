package render

import (
	"context"
	"fmt"
	"sync"

	"loomtale/api/internal/media/ffmpeg"
)

// EncoderInfo is what a render worker found at boot: the encoder auto
// resolves to and why NVENC is unavailable when it is.
type EncoderInfo struct {
	Codec  string `json:"codec"`
	NVENC  bool   `json:"nvenc"`
	Reason string `json:"reason,omitempty"`
}

// EncoderProber runs a one-frame test encode (ffmpeg.Runner does).
type EncoderProber interface {
	ProbeEncoder(ctx context.Context, codec string) error
}

// EncoderProbe probes h264_nvenc once per process and falls back to
// libx264. The result is cached: a GPU does not appear or vanish while
// a worker runs, and the probe costs a process start.
type EncoderProbe struct {
	Prober EncoderProber
	once   sync.Once
	info   EncoderInfo
}

// Info returns the cached probe result, probing on first use.
func (p *EncoderProbe) Info(ctx context.Context) EncoderInfo {
	p.once.Do(func() {
		if err := p.Prober.ProbeEncoder(ctx, ffmpeg.CodecNVENC); err != nil {
			p.info = EncoderInfo{Codec: EncoderX264, Reason: "h264_nvenc test encode failed; using libx264"}
			return
		}
		p.info = EncoderInfo{Codec: EncoderNVENC, NVENC: true}
	})
	return p.info
}

// ResolveEncoder turns the settings' encoder choice into the concrete
// encoder frozen into a manifest. An explicit h264_nvenc on a host
// without it is an error rather than a silent fallback.
func ResolveEncoder(choice string, info EncoderInfo) (string, error) {
	switch choice {
	case EncoderAuto:
		if info.Codec == "" {
			return EncoderX264, nil
		}
		return info.Codec, nil
	case EncoderNVENC:
		if !info.NVENC {
			return "", fmt.Errorf("render: h264_nvenc is not available on the render worker")
		}
		return EncoderNVENC, nil
	case EncoderX264:
		return EncoderX264, nil
	default:
		return "", fmt.Errorf("render: encoder %q is not supported", choice)
	}
}

// encoderProfile names the fixed encoder parameters below. It is part of
// every video segment hash, so changing a parameter must change it and
// thereby invalidate cached segments instead of mixing old and new ones.
const encoderProfile = "h264-high-v1"

// VideoEncode is the segment encoding of codec at fps: identical for
// every segment of a render so the concat demuxer can join them with
// stream copy. A keyframe opens each segment and then every two seconds.
func VideoEncode(codec string, fps int) *ffmpeg.VideoEncode {
	v := &ffmpeg.VideoEncode{
		PixelFormat: "yuv420p", FrameRate: fps, GOP: 2 * fps, ClosedSegment: true, TimeScale: fps * 512,
	}
	if codec == EncoderNVENC {
		v.Preset, v.Quality = "p4", 21
	} else {
		v.Preset, v.Quality = "veryfast", 20
	}
	return v
}

// PreviewEncode is the encoding of the 540p preview proxies: fast and
// small, never joined to anything.
func PreviewEncode(fps int) *ffmpeg.VideoEncode {
	return &ffmpeg.VideoEncode{Preset: "veryfast", Quality: 28, PixelFormat: "yuv420p", FrameRate: fps, GOP: 2 * fps}
}
