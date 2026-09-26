import type { TargetLanguage } from "../../api/gen/types.gen";

/**
 * Client-side mirror of the backend's `duration.Estimate(words, lang,
 * voicePreset)` display formula (phase 6 requirements: EN 150 wpm / VI 165
 * wpm, "uncalibrated" until phase 9b writes measured rates). This is a pure
 * display computation only -- it never calls the API -- so the footer stays
 * live while typing.
 */
const WORDS_PER_MINUTE: Record<TargetLanguage, number> = {
  en: 150,
  vi: 165,
};

export function estimateDurationMinutes(wordCount: number, lang: TargetLanguage): number {
  const wpm = WORDS_PER_MINUTE[lang];
  if (!Number.isFinite(wordCount) || wordCount <= 0) return 0;
  return wordCount / wpm;
}

/** Formats the footer label, e.g. "~30 min (uncalibrated)". */
export function formatDurationEstimate(wordCount: number, lang: TargetLanguage): string {
  const minutes = Math.round(estimateDurationMinutes(wordCount, lang));
  return `~${minutes} min (uncalibrated)`;
}

/** Counts words the same way for every paragraph list (whitespace-delimited, CJK-aware split not needed for EN/VI). */
export function countWords(paragraphs: { text: string }[]): number {
  return paragraphs.reduce((total, paragraph) => {
    const words = paragraph.text.trim().split(/\s+/).filter(Boolean);
    return total + words.length;
  }, 0);
}
