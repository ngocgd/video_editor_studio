// Package ffmpeg is the one place any process of this codebase runs the
// ffmpeg binary. Every argument is built from typed values (never by
// concatenating user text into a command line), every input is opened
// with a forced demuxer and a protocol whitelist, and stderr is capped
// and scrubbed of capability URLs before it is logged. Scene steps, the
// render pipeline and thumbnails all go through Runner.
package ffmpeg

// The only protocol whitelists ffmpeg is ever given. A remote input is an
// internal presigned object URL (https to the configured internal MinIO
// host, checked in Go before exec); a local input is a file inside the
// step's own temp directory. No other protocol — http, concat:, subfile,
// data, pipe — is accepted for an input.
const (
	RemoteProtocolWhitelist = "https,tls,tcp"
	LocalProtocolWhitelist  = "file"
)

// Format is the demuxer forced for an input with -f, so ffmpeg never
// probes the bytes to pick one.
type Format string

const (
	FormatMP4    Format = "mp4"
	FormatWAV    Format = "wav"
	FormatFLAC   Format = "flac"
	FormatMP3    Format = "mp3"
	FormatImage2 Format = "image2"
	FormatConcat Format = "concat"
	FormatSRT    Format = "srt"
)

var knownFormats = map[Format]bool{
	FormatMP4: true, FormatWAV: true, FormatFLAC: true, FormatMP3: true, FormatImage2: true, FormatConcat: true, FormatSRT: true,
}

// FormatForMIME maps an asset's sniffed MIME type to the demuxer forced
// for it. ok is false for a type no step reads through ffmpeg.
func FormatForMIME(mime string) (Format, bool) {
	switch mime {
	case "image/png", "image/jpeg", "image/webp":
		return FormatImage2, true
	case "audio/wav", "audio/x-wav":
		return FormatWAV, true
	case "audio/flac":
		return FormatFLAC, true
	case "audio/mpeg":
		return FormatMP3, true
	case "video/mp4":
		return FormatMP4, true
	default:
		return "", false
	}
}
