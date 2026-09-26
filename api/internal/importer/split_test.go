package importer

import "testing"

func TestSplitChinesePreset(t *testing.T) {
	text := "第一章 开始\n这是第一章的内容。\n第二章 继续\n这是第二章的内容，更长一些。"
	chapters, used := Split(text, PresetChinese)
	if used != PresetChinese {
		t.Fatalf("used preset = %q", used)
	}
	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d: %+v", len(chapters), chapters)
	}
	if chapters[0].Index != 1 || chapters[1].Index != 2 {
		t.Fatalf("chapters not 1-indexed in order: %+v", chapters)
	}
	runes := []rune(text)
	if got := chapters[0].Text(runes); got == "" {
		t.Fatal("expected chapter 1 text to be non-empty")
	}
}

func TestSplitEnglishPreset(t *testing.T) {
	text := "Chapter 1\nOnce upon a time.\nChapter 2\nThe story continues here with more words."
	chapters, used := Split(text, PresetEnglish)
	if used != PresetEnglish {
		t.Fatalf("used preset = %q", used)
	}
	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d: %+v", len(chapters), chapters)
	}
}

func TestSplitVietnamesePreset(t *testing.T) {
	text := "Chương 1\nNgay xua ngay xua.\nChương 2\nCau chuyen tiep tuc."
	chapters, used := Split(text, PresetViet)
	if used != PresetViet {
		t.Fatalf("used preset = %q", used)
	}
	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d: %+v", len(chapters), chapters)
	}
}

func TestSplitAutoPicksPresetWithMostMatches(t *testing.T) {
	text := "Chapter 1\nSome text.\nChapter 2\nMore text.\nChapter 3\nEven more."
	chapters, used := Split(text, PresetAuto)
	if used != PresetEnglish {
		t.Fatalf("auto should pick english for this text, got %q", used)
	}
	if len(chapters) != 3 {
		t.Fatalf("expected 3 chapters, got %d", len(chapters))
	}
}

func TestSplitNoMatchesReturnsWholeText(t *testing.T) {
	text := "Just a plain manuscript with no chapter headings at all."
	chapters, used := Split(text, PresetAuto)
	if used != PresetAuto {
		t.Fatalf("used = %q", used)
	}
	if len(chapters) != 1 {
		t.Fatalf("expected a single whole-text chapter, got %d", len(chapters))
	}
	if chapters[0].WordCount == 0 {
		t.Fatal("expected non-zero word count for the whole-text chapter")
	}
}

func TestSplitPresetWithNoMatchesFallsBackToWholeText(t *testing.T) {
	text := "No headings here."
	chapters, used := Split(text, PresetChinese)
	if used != PresetChinese {
		t.Fatalf("used = %q", used)
	}
	if len(chapters) != 1 {
		t.Fatalf("expected fallback single chapter, got %d", len(chapters))
	}
}
