package bench

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
)

// LLMCase is one writing task the story writer asks an LLM for.
type LLMCase struct {
	Name     string
	Language string
	System   string
	Prompt   string
}

const (
	outlineSystem = "You are the story writer of a xianxia fiction YouTube channel. Write in the requested language only. Reply with the requested text and nothing else."
	premiseEN     = "A disciple with a broken meridian secretly carries the lost Frost Phoenix flame. An elder discovers it, rivals want the technique, and a letter from his dead mother sends him to the Valley of Ten Thousand Swords."
	premiseVI     = "Một đệ tử có kinh mạch tổn thương bí mật mang ngọn lửa Băng Phượng đã thất truyền. Một trưởng lão phát hiện ra, các đối thủ muốn cướp công pháp, và lá thư của người mẹ đã mất đưa cậu tới Vạn Kiếm Cốc."
)

// LLMCases are the outline and draft samples rated against claude CLI.
var LLMCases = []LLMCase{
	{Name: "outline-en", Language: "en", System: outlineSystem, Prompt: "Write an episode outline of 8 numbered beats, one or two sentences each, for this premise:\n" + premiseEN},
	{Name: "draft-en", Language: "en", System: outlineSystem, Prompt: "Write the opening scene of the episode as narration, about 400 words, for this premise:\n" + premiseEN},
	{Name: "outline-vi", Language: "vi", System: outlineSystem, Prompt: "Viết dàn ý một tập truyện gồm 8 ý được đánh số, mỗi ý một hoặc hai câu, bằng tiếng Việt, cho cốt truyện sau:\n" + premiseVI},
	{Name: "draft-vi", Language: "vi", System: outlineSystem, Prompt: "Viết cảnh mở đầu của tập truyện dưới dạng lời kể, khoảng 400 chữ, bằng tiếng Việt, cho cốt truyện sau:\n" + premiseVI},
}

// LLMTarget is one provider under test. Residency, when set, is loaded
// through the residency manager first (the local Ollama model), so the
// load is timed as a residency switch and proven through /api/ps.
type LLMTarget struct {
	Name      string
	Provider  llm.Provider
	Residency *pipeline.ModelRef
}

// LLMResult is one generation.
type LLMResult struct {
	Target, Case    string
	FirstTokenS     float64
	TotalS          float64
	OutTokens       int
	TokensPerSecond float64
	// Streamed is false when the whole answer arrived in one chunk.
	Streamed      bool
	SwitchSeconds float64
	Switched      bool
	Text          string
	Err           error
}

// LLMRunner runs the llm suite.
type LLMRunner struct {
	Residency pipeline.ModelResidency
	Queries   dbgen.Querier
	// OutDir receives every output as markdown plus ratings.csv, the
	// sheet a person fills with 1-5 quality ratings.
	OutDir string
	Log    func(format string, args ...any)
}

// Run generates every case with every target.
func (r *LLMRunner) Run(ctx context.Context, targets []LLMTarget) ([]LLMResult, []Budget, error) {
	runID := idconv.NewV7()
	var results []LLMResult
	for _, t := range targets {
		var switchS float64
		var switched bool
		var loadErr error
		if t.Residency != nil {
			if current := r.Residency.Current(); current == nil || *current != *t.Residency {
				start := time.Now()
				loadErr = r.Residency.Ensure(ctx, *t.Residency)
				switchS, switched = time.Since(start).Seconds(), loadErr == nil
			}
		}
		for i, c := range LLMCases {
			res := LLMResult{Target: t.Name, Case: c.Name}
			if i == 0 {
				res.SwitchSeconds, res.Switched = switchS, switched
			}
			if loadErr != nil {
				res.Err = fmt.Errorf("bench: load %s: %w", t.Name, loadErr)
			} else {
				r.generate(ctx, t.Provider, c, &res)
			}
			results = append(results, res)
			if err := r.record(ctx, runID, res); err != nil {
				return results, nil, err
			}
			if ctx.Err() != nil {
				return results, nil, ctx.Err()
			}
		}
	}
	if err := r.writeOutputs(results); err != nil {
		return results, nil, err
	}
	return results, llmBudgets(results), nil
}

func (r *LLMRunner) generate(ctx context.Context, p llm.Provider, c LLMCase, res *LLMResult) {
	start := time.Now()
	var first time.Duration
	resp, err := p.Stream(ctx, llm.Request{
		System: c.System, Messages: []llm.Message{{Role: "user", Text: c.Prompt}}, MaxTokens: 1200, Temperature: 0.7,
	}, func(d llm.Delta) {
		if first == 0 && d.Text != "" {
			first = time.Since(start)
		}
	})
	res.TotalS = time.Since(start).Seconds()
	if err != nil {
		res.Err = err
		return
	}
	res.FirstTokenS, res.OutTokens, res.Text = first.Seconds(), resp.Usage.Out, resp.Text
	// A provider that answers in one chunk (the claude CLI sidecar) has
	// no generation phase after its first token, so its throughput is
	// taken over the whole request instead.
	gen := res.TotalS - res.FirstTokenS
	res.Streamed = gen > 0.05*res.TotalS
	if !res.Streamed {
		gen = res.TotalS
	}
	if gen > 0 && res.OutTokens > 0 {
		res.TokensPerSecond = float64(res.OutTokens) / gen
	}
}

func (r *LLMRunner) record(ctx context.Context, runID uuid.UUID, res LLMResult) error {
	if res.Err != nil {
		r.logf("%-12s %-14s FAILED: %v", res.Case, res.Target, res.Err)
	} else {
		r.logf("%-12s %-14s first token %5.2fs  %6.1f tok/s  %5d tokens  %6.1fs total", res.Case, res.Target, res.FirstTokenS, res.TokensPerSecond, res.OutTokens, res.TotalS)
	}
	return recordRow(ctx, r.Queries, runID, row{
		Suite: "llm", Case: res.Case, Model: res.Target, Seconds: res.TotalS,
		SwitchSeconds: res.SwitchSeconds, Switched: res.Switched, Err: res.Err,
		Meta: map[string]any{"first_token_s": res.FirstTokenS, "tokens_per_s": res.TokensPerSecond, "out_tokens": res.OutTokens, "streamed": res.Streamed},
	})
}

// writeOutputs saves each generation for human rating and a ratings
// sheet with one empty row per generation.
func (r *LLMRunner) writeOutputs(results []LLMResult) error {
	if r.OutDir == "" {
		return nil
	}
	dir := filepath.Join(r.OutDir, "llm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "ratings.csv"))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"case", "target", "file", "rating_1_to_5", "notes"})
	for _, res := range results {
		if res.Err != nil {
			continue
		}
		name := res.Target + "-" + res.Case + ".md"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(res.Text), 0o644); err != nil {
			return err
		}
		_ = w.Write([]string{res.Case, res.Target, name, "", ""})
	}
	w.Flush()
	return w.Error()
}

func llmBudgets(results []LLMResult) []Budget {
	var budgets []Budget
	seen := map[string]bool{}
	for _, res := range results {
		if seen[res.Target] {
			continue
		}
		seen[res.Target] = true
		first := Budget{Name: res.Target + " first token (worst)", Limit: BudgetLLMFirstTokenS, Unit: "s"}
		switchB := Budget{Name: res.Target + " residency switch", Limit: BudgetResidencySwitchS, Unit: "s"}
		for _, o := range results {
			if o.Target != res.Target {
				continue
			}
			if o.Err == nil {
				first.Measured, first.Known = max(first.Measured, o.FirstTokenS), true
			}
			if o.Switched {
				switchB.Measured, switchB.Known = o.SwitchSeconds, true
			}
		}
		budgets = append(budgets, first)
		if switchB.Known {
			budgets = append(budgets, switchB)
		}
	}
	return budgets
}

func (r *LLMRunner) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}
