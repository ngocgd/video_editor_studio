import { useState } from "react";

import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";

export interface GlossaryTerm {
  termZh: string;
  en: string;
  vi: string;
}

/** Parses the glossary section's JSON-encoded content; an empty/invalid string renders an empty table instead of crashing (phase 6 requirement). */
export function parseGlossary(content: string): GlossaryTerm[] {
  if (!content.trim()) return [];
  try {
    const parsed = JSON.parse(content) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter((entry): entry is Record<string, unknown> => typeof entry === "object" && entry !== null)
      .map((entry) => ({
        termZh: typeof entry.termZh === "string" ? entry.termZh : "",
        en: typeof entry.en === "string" ? entry.en : "",
        vi: typeof entry.vi === "string" ? entry.vi : "",
      }));
  } catch {
    return [];
  }
}

export function stringifyGlossary(terms: GlossaryTerm[]): string {
  return JSON.stringify(terms);
}

/** Term (ZH) -> EN/VI renderings table (phase 6: matters for Chinese names). */
export function GlossaryTable({ content, onSave }: { content: string; onSave: (nextContent: string) => void }) {
  const [terms, setTerms] = useState<GlossaryTerm[]>(() => parseGlossary(content));

  const updateTerm = (index: number, field: keyof GlossaryTerm, value: string) => {
    setTerms((current) => current.map((t, i) => (i === index ? { ...t, [field]: value } : t)));
  };

  const addRow = () => setTerms((current) => [...current, { termZh: "", en: "", vi: "" }]);
  const removeRow = (index: number) => setTerms((current) => current.filter((_, i) => i !== index));
  const save = () => onSave(stringifyGlossary(terms.filter((t) => t.termZh.trim())));

  return (
    <div className="flex flex-col gap-2">
      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="border-b border-border text-left text-2xs uppercase tracking-[0.04em] text-muted-foreground">
            <th className="h-7 px-2">Term (ZH)</th>
            <th className="h-7 px-2">EN</th>
            <th className="h-7 px-2">VI</th>
            <th className="h-7 px-2" />
          </tr>
        </thead>
        <tbody>
          {terms.map((term, index) => (
            <tr key={index} className="border-b border-border">
              <td className="p-1">
                <Input value={term.termZh} onChange={(e) => updateTerm(index, "termZh", e.target.value)} />
              </td>
              <td className="p-1">
                <Input value={term.en} onChange={(e) => updateTerm(index, "en", e.target.value)} />
              </td>
              <td className="p-1">
                <Input value={term.vi} onChange={(e) => updateTerm(index, "vi", e.target.value)} />
              </td>
              <td className="p-1">
                <Button variant="ghost" size="sm" onClick={() => removeRow(index)} aria-label="Remove term">
                  ×
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="flex gap-2">
        <Button variant="secondary" size="sm" onClick={addRow}>
          Add term
        </Button>
        <Button variant="primary" size="sm" onClick={save}>
          Save glossary
        </Button>
      </div>
    </div>
  );
}
