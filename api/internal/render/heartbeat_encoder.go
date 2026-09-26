package render

import (
	"context"

	"loomtale/api/internal/providers/workerstatus"
)

// HeartbeatEncoder is the EncoderSource of a process that runs no
// render step (the api): it reads the encoder the render worker probed
// and published in its worker_status heartbeat.
type HeartbeatEncoder struct {
	Store *workerstatus.Store
}

// RenderEncoder implements EncoderSource. A missing, stale or
// probe-less heartbeat reports nothing, so auto resolves to libx264.
func (h HeartbeatEncoder) RenderEncoder(ctx context.Context) (EncoderInfo, bool) {
	if h.Store == nil {
		return EncoderInfo{}, false
	}
	st, err := h.Store.Get(ctx)
	if err != nil || !st.Fresh || st.GPU.Encoder == nil {
		return EncoderInfo{}, false
	}
	e := st.GPU.Encoder
	return EncoderInfo{Codec: e.Codec, NVENC: e.NVENC, Reason: e.Reason}, true
}

// ProbeEncoder is the EncoderSource of a render worker: its own probe.
type ProbeEncoder struct {
	Probe *EncoderProbe
}

// RenderEncoder implements EncoderSource.
func (p ProbeEncoder) RenderEncoder(ctx context.Context) (EncoderInfo, bool) {
	if p.Probe == nil {
		return EncoderInfo{}, false
	}
	return p.Probe.Info(ctx), true
}

// HeartbeatInfo converts a probe result for the worker_status heartbeat.
func HeartbeatInfo(info EncoderInfo) *workerstatus.Encoder {
	return &workerstatus.Encoder{Codec: info.Codec, NVENC: info.NVENC, Reason: info.Reason}
}
