package importer

import (
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestDecodeTextUTF8(t *testing.T) {
	text, enc, err := DecodeText([]byte("Chapter 1\nHello world."))
	if err != nil {
		t.Fatal(err)
	}
	if enc != EncodingUTF8 {
		t.Fatalf("encoding = %q, want %q", enc, EncodingUTF8)
	}
	if text != "Chapter 1\nHello world." {
		t.Fatalf("text = %q", text)
	}
}

func TestDecodeTextUTF8BOM(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("Chapter 1")...)
	text, enc, err := DecodeText(raw)
	if err != nil {
		t.Fatal(err)
	}
	if enc != EncodingUTF8 {
		t.Fatalf("encoding = %q, want %q", enc, EncodingUTF8)
	}
	if text != "Chapter 1" {
		t.Fatalf("text = %q, want no BOM prefix", text)
	}
}

func TestDecodeTextUTF16LEBOM(t *testing.T) {
	// "Hi" in UTF-16LE with BOM.
	raw := []byte{0xFF, 0xFE, 'H', 0x00, 'i', 0x00}
	text, enc, err := DecodeText(raw)
	if err != nil {
		t.Fatal(err)
	}
	if enc != EncodingUTF16LE {
		t.Fatalf("encoding = %q, want %q", enc, EncodingUTF16LE)
	}
	if text != "Hi" {
		t.Fatalf("text = %q, want %q", text, "Hi")
	}
}

func TestDecodeTextGB18030(t *testing.T) {
	original := "第一章 开始\n这是一个测试章节。"
	encoded, err := simplifiedchinese.GB18030.NewEncoder().String(original)
	if err != nil {
		t.Fatal(err)
	}
	text, enc, err := DecodeText([]byte(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if enc != EncodingGB18030 {
		t.Fatalf("encoding = %q, want %q", enc, EncodingGB18030)
	}
	if text != original {
		t.Fatalf("text = %q, want %q", text, original)
	}
}

func TestDecodeTextRejectsGarbage(t *testing.T) {
	// Random high bytes that are neither valid UTF-8 nor plausible
	// GB18030 text.
	raw := []byte{0x80, 0x81, 0x82, 0xFF, 0xFF, 0xFF, 0x00, 0x00}
	if _, _, err := DecodeText(raw); err != ErrUndetectedEncoding {
		t.Fatalf("expected ErrUndetectedEncoding, got %v", err)
	}
}

func TestDecodeTextRejectsWindows1252ProseInsteadOfReadingItAsGB18030(t *testing.T) {
	// Every accented letter here pairs with its neighbour into a valid
	// GBK ideograph, so the bytes decode as GB18030 without a single
	// replacement character; only the letter mix gives it away.
	raw, err := charmap.Windows1252.NewEncoder().String("Le château était célèbre pour ses fêtes élégantes et ses légendes.")
	if err != nil {
		t.Fatal(err)
	}
	if _, enc, err := DecodeText([]byte(raw)); err != ErrUndetectedEncoding {
		t.Fatalf("expected ErrUndetectedEncoding, got encoding %q err %v", enc, err)
	}
}
