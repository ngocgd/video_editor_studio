package llm

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// nonceBytes is the number of random bytes used for each request's fence
// nonce (16 hex chars, per the phase 4 contract).
const nonceBytes = 8

// NewNonce returns a fresh random 16-hex-character nonce for fencing one
// request's DataBlocks.
func NewNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("llm: generate nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// RenderDataBlocks renders every block as <data-{nonce}>...</data-{nonce}>,
// escaping any literal occurrence of that exact tag inside the block's own
// text so untrusted content can never forge a closing tag and "escape" the
// fence. The nonce is random per request (see NewNonce), so a block cannot
// know it in advance.
func RenderDataBlocks(nonce string, blocks []DataBlock) string {
	openPrefix := "<data-" + nonce // no trailing '>': an optional label attribute goes before it
	open := openPrefix + ">"
	close_ := "</data-" + nonce + ">"

	var b strings.Builder
	for _, blk := range blocks {
		b.WriteString(openPrefix)
		if blk.Label != "" {
			b.WriteString(" label=\"")
			b.WriteString(escapeAttr(blk.Label))
			b.WriteString("\"")
		}
		b.WriteString(">\n")
		b.WriteString(escapeFence(blk.Text, open, close_))
		b.WriteString("\n")
		b.WriteString(close_)
		b.WriteString("\n")
	}
	return b.String()
}

// escapeFence neutralizes any accidental or adversarial occurrence of the
// fence's own open/close tags inside untrusted text, so a DataBlock can
// never inject a fake closing tag followed by attacker-authored
// "instructions" that would otherwise appear to sit outside the fence.
func escapeFence(text, open, closeTag string) string {
	text = strings.ReplaceAll(text, open, escapeTag(open))
	text = strings.ReplaceAll(text, closeTag, escapeTag(closeTag))
	return text
}

// escapeTag defangs a tag by inserting a zero-width-safe marker between
// '<' and the rest, so it renders visibly as literal text instead of a
// structural fence boundary.
func escapeTag(tag string) string {
	return strings.Replace(tag, "<", "&lt;", 1)
}

func escapeAttr(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

// AnyTainted reports whether any block is tainted; used by callers (phase
// 6+) to propagate taint onto content derived from a request's blocks.
func AnyTainted(blocks []DataBlock) bool {
	for _, b := range blocks {
		if b.Tainted {
			return true
		}
	}
	return false
}
