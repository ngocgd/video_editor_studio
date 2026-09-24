# Loomtale Studio — Design Guidelines

Status: v0.1 (2026-09-24) · Theme: dark only (light slot reserved) · Stack: React + Vite + shadcn/ui (Radix + Tailwind)
Wireframes: [`docs/wireframe/`](wireframe/) · Logo: [`docs/wireframe/logo.svg`](wireframe/logo.svg)

## 1. Direction

A calm, precise craft tool for many-hour sessions editing text, images and audio. References: DaVinci Resolve (dense panels,
sunken media wells), Descript (text-as-timeline), Linear (keyboard-first, quiet chrome).

Principles:
1. **Content is the colour.** Chrome is neutral graphite; scene images and waveforms carry the colour.
2. **One accent, used sparingly.** Jade marks the primary action, the current selection and focus. Nothing else.
3. **State over decoration.** Every scene, job and upload shows its real state (done, queued, running, failed, stale).
4. **Dense but legible.** 13px UI text and 28–32px rows, with AA contrast everywhere.
5. **Keyboard first.** Every frequent action has a shortcut, and `Ctrl K` opens the command palette.

## 2. Colour tokens

Tokens are CSS variables on `:root` (dark). A future light theme adds `[data-theme="light"]` overriding the **same names**, so no component
changes. Values are hex here; shadcn v4 accepts any CSS colour, so hex is fine (convert to OKLCH only if needed).

### 2.1 Surfaces and text

| Role | CSS var (shadcn) | Hex | Use |
|---|---|---|---|
| Well (sunken) | `--well` | `#0E0F11` | Viewer, timeline, inputs, editor page |
| Panel background | `--background` | `#141518` | App panels, nav, inspector |
| Raised | `--card` | `#1B1C20` | Cards, scene tiles, table rows on hover |
| Overlay | `--popover` | `#222328` | Menus, popovers, dialogs, tooltips |
| Hover / pressed | `--accent` | `#2A2B31` | shadcn "accent" = neutral hover fill (NOT brand) |
| Secondary button | `--secondary` | `#222328` | Neutral buttons |
| Muted fill | `--muted` | `#1B1C20` | Skeletons, disabled fills |
| Border subtle | `--border` | `#34363C` | Panel dividers, card outlines (decorative) |
| Input border | `--input` | `#686A72` | Form control edges (3.4:1 on panel, meets 1.4.11) |
| Text | `--foreground` | `#E6E4DF` | Primary text (14.4:1 on panel) |
| Text secondary | `--text-2` | `#A8A6A0` | Labels, metadata (7.5:1) |
| Text muted | `--muted-foreground` | `#95928C` | Hints, timestamps (>= 4.55:1 on every surface incl. hover) |

Text is warm off-white, never `#FFFFFF`; backgrounds are neutral graphite, never blue-black.

### 2.2 Accent and status

| Role | CSS var | Hex | Contrast on panel | Rule |
|---|---|---|---|---|
| Accent (jade) | `--primary` | `#4DB6A0` | 7.4:1 | Primary button fill, selection outline, active nav marker |
| Accent text/ring | `--ring`, `--primary-text` | `#6CC9B4` | 9.3:1 | Focus ring, accent links |
| On accent | `--primary-foreground` | `#0E0F11` | 7.8:1 on jade | Text on jade buttons |
| Success | `--success` | `#6BBF73` | 8.1:1 | Step done |
| Warning | `--warning` | `#D9A441` | 8.1:1 | Stale output, private-only upload, VRAM tight |
| Danger | `--destructive` | `#EE6B61` | 6.0:1 | Failed job, destructive action |
| Info / running | `--info` | `#6FA8DC` | 7.2:1 | Running job, progress |

Tinted badge fills use the status colour at 14% alpha (`--success-muted`, etc.). **Status is never colour-only**: always pair with an icon
and/or a word. Jade and success are separated by hue (167° vs 125°) and role: jade never means "done".

### 2.3 Data colours (speakers / timeline tracks)

Desaturated, distinguishable, not used for UI chrome: `--spk-narrator #95928C`, `--spk-1 #C9A26B` (ochre), `--spk-2 #7FA7C9` (steel),
`--spk-3 #C98A9E` (rose), `--spk-4 #9DB57A` (moss), `--spk-5 #B59BD0` (lilac, only as the 5th track). Each speaker also gets a
two-letter monogram so tracks remain identifiable without colour.

### 2.4 Tailwind mapping

```css
@theme inline {
  --color-background: var(--background); --color-foreground: var(--foreground);
  --color-well: var(--well); --color-card: var(--card); --color-popover: var(--popover);
  --color-primary: var(--primary); --color-primary-foreground: var(--primary-foreground);
  --color-success: var(--success); --color-warning: var(--warning); --color-info: var(--info);
  --color-destructive: var(--destructive); --color-border: var(--border); --color-input: var(--input); --color-ring: var(--ring);
  --font-sans: "IBM Plex Sans", system-ui, sans-serif; --font-mono: "IBM Plex Mono", ui-monospace, monospace;
  --font-serif: "Literata", Georgia, serif;
  --radius-sm: 3px; --radius-md: 5px; --radius-lg: 8px;
}
```

## 3. Typography

All three families are verified on Google Fonts with the `vietnamese` subset (checked 2026-09-24).

| Family | Role | Why |
|---|---|---|
| **IBM Plex Sans** 400/500/600 | All UI | Engineered, slightly technical grotesque; reads as "tool", not "startup"; strong Vietnamese diacritics; tabular figures |
| **IBM Plex Mono** 400/500 | Timecodes, durations, seeds, model IDs, prompts, keycaps | Same skeleton as Plex Sans, so mono data sits quietly beside UI text |
| **Literata** (opsz) 400/500 + italic | Story writer and narration text | Designed for long-form screen reading; optical sizes; excellent Vietnamese stacking (ẩ, ệ, ữ) at body size |

Load: `family=IBM+Plex+Sans:wght@400;500;600&family=IBM+Plex+Mono:wght@400;500&family=Literata:ital,opsz,wght@0,7..72,400;0,7..72,500;1,7..72,400&display=swap`.

| Token | Size / line-height | Weight | Use |
|---|---|---|---|
| `text-2xs` | 11 / 14 | 500, +0.04em, uppercase | Section eyebrows, column heads (sparingly) |
| `text-xs` | 12 / 16 | 400 | Metadata, badges, status bar |
| `text-sm` (UI base) | 13 / 18 | 400/500 | Default UI text, inputs, rows |
| `text-md` | 14 / 20 | 500 | Panel titles, dialog body |
| `text-lg` | 16 / 22 | 600 | Page titles |
| `text-xl` | 20 / 26 | 600 | Dashboard stat values (Plex Mono) |
| `read-md` | 17 / 1.7 (Literata) | 400 | Narration in inspector |
| `read-lg` | 19 / 1.75 (Literata) | 400 | Draft editor, max 68ch |

Vietnamese: keep line-height >= 1.5 for Plex UI text in multi-line blocks and >= 1.7 for Literata so stacked marks do not collide.
Use `font-variant-numeric: tabular-nums` for every timecode, counter and progress figure.

## 4. Spacing, radius, elevation, motion

- **Spacing** (4px base): `0.5=2 · 1=4 · 1.5=6 · 2=8 · 3=12 · 4=16 · 5=20 · 6=24 · 8=32`. Panel padding 12; row height 28 (dense) / 32 (default);
  control height 28 (sm) / 32 (md). Touch-size (44px) targets are required only for the future mobile review view.
- **Radius**: `sm 3px` (badges, keycaps), `md 5px` (buttons, inputs, tiles), `lg 8px` (dialogs, popovers). No pills except
  toggle-switch tracks and avatar circles.
- **Elevation**: panels are separated by 1px borders, not shadows. Only overlays get a shadow: `--shadow-overlay: 0 8px 24px rgba(0,0,0,.45), 0 0 0 1px #34363C`.
- **Motion**: 120ms `ease-out` for hover/press, 180ms for panels/popovers. Progress bars animate width only. No parallax, bounce or
  shimmer in the UI chrome (Ken Burns/parallax belong to the rendered video preview only). Honour `prefers-reduced-motion`: disable
  spinners' rotation (show a static dashed ring + text) and all transitions.

## 5. Iconography

Lucide (`lucide-react`), 16px in UI rows and 14px in badges, `stroke-width 1.75`, `currentColor`. Icons always come with a label or
`aria-label` and a tooltip for icon-only buttons. No emoji anywhere in the UI. AI actions use `wand-sparkles` only on the AI menu trigger, not
sprinkled across every button. Status icons are fixed: done `circle-check`, running `loader-circle`, queued `clock`, failed `circle-x`,
stale `triangle-alert`, not started `circle-dashed`, locked/private `lock`.

## 6. Layout shell

```
+--------+----------------------------------------------------+-------------+
| Nav    | Top bar 44px: breadcrumb · page tabs · Ctrl K · user              |
| 56 or  +----------------------------------------------------+-------------+
| 224px  | Main (scene grid / editor / tables)                | Inspector   |
|        |                                                    | 360px       |
|        +----------------------------------------------------+ resizable   |
|        | Timeline / secondary strip (optional, 168px)       | 320–480px   |
+--------+----------------------------------------------------+-------------+
| Status bar 28px: GPU slot · queue count · VRAM · LLM provider · save state|
+---------------------------------------------------------------------------+
```

- **Left nav**: global sections (Dashboard, Projects, Import, Review, Library, Analytics, Settings); inside a project, a second group
  (Bible, Characters, Episodes, Storyboard, Render, Publish). Collapses to a 56px icon rail with `Ctrl \`.
- **Inspector**: context panel for the current selection; collapsible with `Ctrl .`. Never a modal for per-item editing.
- **Status bar**: always visible, and the single source for the GPU queue (one slot). Clicking opens the Render Queue.
- Minimum supported width 1280px for the editors; below 1024 the inspector becomes a sheet and the nav collapses to the rail.

## 7. Component patterns

**Scene tile** (storyboard grid): 16:9 thumbnail (sunken well when empty) · index `#012` + timecode in mono · first line of narration
(2 lines, clamp) · pipeline pips `TXT IMG VOI SUB MOT` each with its status icon · speaker monogram. Selected = 2px jade outline;
multi-select = jade check in corner. Stale (text edited after image/voice generated) = warning triangle on the affected pip with tooltip
"Narration changed after voice was generated".

**Pipeline pip / status badge**: `[icon] LABEL` at 12px, tinted fill. States: `done`, `running 43%`, `queued #3`, `failed`, `stale`,
`none`. Row-level status uses the worst state of its pips.

**Job progress**: name · step · determinate bar (4px, `--info`) · `ETA 02:14` mono · cancel. Indeterminate only while waiting on the GPU,
labelled "Waiting for GPU slot (#2)". Failed jobs keep the bar at the failure point in `--destructive` with "Retry step" and "View log".

**GPU queue indicator** (status bar): `GPU [running job name] 61% · 3 queued · VRAM 13.8/16 GB`. VRAM bar turns warning at >= 90%.
Hover shows the ordered queue; the running model name is shown (`Z-Image Turbo`, `Chatterbox`, `VieNeu-TTS`).

**Inline AI actions**: a quiet toolbar on text selection (`Rewrite`, `Expand`, `Shorten`, `Continue`) plus a panel footer with the
provider/model picker (`Ollama · gemma4:12b-q6` / `Claude CLI · sonnet`). Results arrive as a **diff proposal** (green insert / struck
removal) with `Accept Tab`, `Reject Esc`, `Retry`. AI output never silently replaces user text. Show provider and token/time cost afterwards.

**Regenerate (per scene)**: `Regenerate image` / `Regenerate voice` buttons live in the inspector, queue a single-scene GPU job, keep the previous
take in a "Takes" strip (A/B compare, revert).

**Empty / loading / error states**
- Empty: one sentence explaining what fills this area + one primary action (e.g. "No scenes yet. Split the draft into scenes." `[Split draft]`).
  No illustrations.
- Loading: skeleton blocks in `--muted` matching final layout; for GPU work show the queue position instead of a spinner.
- Error: inline, next to the failing object, with the cause in plain words, the log excerpt in mono and `Retry` / `Open log`. Toasts only for
  background completions ("Episode 3 voice finished").

**Publish state honesty**: while the Google Cloud project is unaudited, every upload is shown as `Private (API not audited)` with a lock icon
and a warning note. The "Public" and "Scheduled public" options are disabled with the reason in text, not hidden.

## 8. Keyboard shortcuts (defaults)

| Scope | Keys |
|---|---|
| Global | `Ctrl K` palette · `Ctrl \` nav rail · `Ctrl .` inspector · `G then D/P/R/S` go to Dashboard/Projects/Review/Settings · `?` shortcut sheet |
| Storyboard | `J / K` prev/next scene · `Space` play scene audio · `I` regen image · `V` regen voice · `E` edit narration · `M` cycle motion preset · `Shift+Click` range select |
| Writer | `Ctrl Enter` continue · `Ctrl Shift R` rewrite selection · `Ctrl Shift E` expand outline beat · `Tab` accept proposal · `Esc` reject · `Ctrl Alt L` EN/VI |

Show shortcuts in tooltips and menus as mono keycaps (`kbd`: 11px Plex Mono, `--popover` fill, 1px `--border`, radius sm).

## 9. Accessibility

- WCAG 2.1 AA: all text >= 4.5:1 (see tables); UI component boundaries and focus indicators >= 3:1.
- Focus: `outline: 2px solid var(--ring); outline-offset: 2px` on every interactive element; never removed.
- Status conveyed by icon + text, never colour only; speaker tracks carry monograms.
- All regions are landmarks (`nav`, `main`, `aside[aria-label=Inspector]`, `footer[role=status]` with `aria-live="polite"` for job completion).
- Grids of scenes use roving tabindex with arrow keys; tiles have `aria-selected`.
- Language: set `lang="vi"` on Vietnamese content blocks so screen readers and hyphenation behave.
- Audio: every waveform action has a keyboard equivalent; subtitles/narration text is always visible alongside audio.

## 10. Do / Don't (anti-slop list)

| Do | Don't |
|---|---|
| Neutral graphite panels with 1px dividers | Purple/blue gradients, glows, aurora backgrounds |
| One jade accent for primary + selection | Multiple competing accent colours |
| Opaque surfaces | Glassmorphism, backdrop blur |
| Lucide line icons with labels | Emoji icons, 3D icons, sparkles on every button |
| 5px radius, square-ish tiles | Rounded-everything bubbly cards, pill buttons |
| Real data: timecodes, VRAM, queue position | Vanity hero cards with huge numbers and no action |
| Plex Sans / Plex Mono / Literata | Inter/Poppins defaults |
| Dense rows, clear hierarchy by weight and colour | Oversized padding, centred marketing layouts inside the tool |
| AI results as reviewable diffs | Auto-replacing user text, "magic" without provenance |
| Honest states: private-only, stale, failed | Hiding disabled options or faking success |

## 11. Wireframe index

| File | Fidelity |
|---|---|
| `wireframe/storyboard-scene-editor.html` | High |
| `wireframe/story-writer.html` | High |
| `wireframe/app-shell-dashboard.html` | Medium |
| `wireframe/render-queue.html`, `characters.html`, `review-publish.html`, `settings-models.html` | Low–medium |

Screenshots at 1440×900: [`docs/wireframes/*.png`](wireframes/) (plus `storyboard-scene-editor-inspector-scrolled.png` for the lower inspector).
Wireframes inline the same token block; when a token changes here, update the `:root` block in each file (they are static mockups, not app code).
