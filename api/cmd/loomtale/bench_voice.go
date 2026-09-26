package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"loomtale/api/internal/bench"
	"loomtale/api/internal/models"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/align"
	"loomtale/api/internal/providers/llm/claudecli"
	"loomtale/api/internal/providers/llm/ollama"
	"loomtale/api/internal/providers/pyworker"
	"loomtale/api/internal/providers/residency"
	"loomtale/api/internal/providers/train"
	"loomtale/api/internal/providers/tts"
	"loomtale/api/internal/providers/vision"
	"loomtale/api/internal/providers/workerconn"
	"loomtale/api/internal/secretstr"
	"loomtale/api/internal/speechrate"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// voiceSuites are run by runVoiceBench rather than the ComfyUI harness.
var voiceSuites = []string{"tts", "align", "llm", "voice-smoke", "vision", "train", "train-smoke"}

// refSuites read their input images from the --refs directory.
var refSuites = map[string]bool{"vision": true, "train": true, "train-smoke": true}

func isVoiceSuite(name string) bool {
	for _, s := range voiceSuites {
		if s == name {
			return true
		}
	}
	return false
}

// runVoiceBench runs the tts, align, llm, voice-smoke, vision, train and
// train-smoke suites against the Python worker and Ollama through the
// residency manager. Needs DATABASE_URL, MODELS_DIR, PYWORKER_ADDR and
// PYWORKER_BEARER_TOKEN_PATH; OLLAMA_URL for the Ollama cases;
// LLMCLI_URL and LLMCLI_BEARER_TOKEN_PATH to compare against claude CLI.
// The vision and train suites read reference images from refsDir.
func runVoiceBench(ctx context.Context, suite, outDir, ollamaModel, refsDir string) error {
	var refs []bench.RefImage
	if refSuites[suite] {
		var err error
		if refs, err = bench.LoadRefImages(refsDir); err != nil {
			return err
		}
	}
	m, store, dir, closeDB, err := modelsEnv(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	addr := os.Getenv("PYWORKER_ADDR")
	if addr == "" {
		return errRequiredEnv("PYWORKER_ADDR")
	}
	token := readSecretFile(os.Getenv("PYWORKER_BEARER_TOKEN_PATH"))
	if token == "" {
		return errRequiredEnv("PYWORKER_BEARER_TOKEN_PATH")
	}
	reserveMB := int64(1024)
	if v := os.Getenv("RENDER_RESERVE_MB"); v != "" {
		if reserveMB, err = strconv.ParseInt(v, 10, 64); err != nil {
			return fmt.Errorf("RENDER_RESERVE_MB: %w", err)
		}
	}

	conn, err := workerconn.Dial(addr, secretstr.String(token))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	pw := pyworker.New(workerv1.NewWorkerClient(conn))
	gate := &models.LoadGate{Manifest: m, Store: store, Dir: dir}
	backends := map[string]residency.Backend{"pyworker": &pyworker.Backend{Client: pw, Gate: gate.Check}}
	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL != "" {
		modelfiles, err := models.EmbeddedModelfiles()
		if err != nil {
			return err
		}
		preparer := &models.OllamaPreparer{
			Manifest: m, Modelfiles: modelfiles, Gate: gate.Check, Dir: dir,
			Importer: &ollama.Importer{BaseURL: ollamaURL, Client: benchHTTPClient()},
		}
		backends["ollama"] = &ollama.Backend{Provider: ollama.New(ollamaURL, ollamaModel, benchHTTPClient()), Prepare: preparer.Prepare}
	}

	unlock, err := lockGPUSlot(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer unlock()
	probe := &pyworker.Probe{Client: pw, RenderReserveMB: reserveMB}
	manager, err := residency.NewManagerWithBudget(ctx, probe, backends, m.VRAMByRef(), reserveMB)
	if err != nil {
		return err
	}
	defer func() { _ = manager.UnloadAll(context.WithoutCancel(ctx)) }()
	fmt.Printf("bench %s: VRAM budget %d MB (free at start minus %d MB render reserve)\n", suite, manager.BudgetMB, reserveMB)
	if outDir != "" {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
	}
	logf := func(format string, args ...any) { fmt.Printf(format+"\n", args...) }

	if suite == "llm" {
		return runLLMSuite(ctx, manager, store, outDir, ollamaURL, ollamaModel, logf)
	}

	host, err := bench.AdvertiseHost(addr)
	if err != nil {
		return fmt.Errorf("bench: find this container's address on the worker network: %w", err)
	}
	sink, err := bench.NewSink(host)
	if err != nil {
		return err
	}
	defer func() { _ = sink.Close() }()
	runner := &bench.VoiceRunner{
		TTS: tts.New(workerv1.NewTTSClient(conn)), Align: align.New(workerv1.NewAlignClient(conn)),
		Vision: vision.New(workerv1.NewVisionClient(conn)), Train: train.New(workerv1.NewTrainClient(conn)),
		Residency: manager, Queries: store.Queries, Sink: sink,
		Rates: &speechrate.Store{Queries: store.Queries}, OutDir: outDir, Log: logf,
	}

	var failed int
	var budgets []bench.Budget
	switch suite {
	case "tts":
		report, err := runner.RunTTS(ctx)
		if err != nil {
			return err
		}
		failed, budgets = countFailed(report.Results), report.Budgets
		for _, c := range report.Calibrations {
			fmt.Printf("calibration %-40s %5d words in %7.1fs: %6.1f wpm", c.VoiceKey, c.Words, c.Seconds, c.WPM)
			if c.HoldoutChecked {
				fmt.Printf("  held-out prediction %.1fs vs %.1fs actual (%.1f%%)", c.HoldoutPredictedS, c.HoldoutActualS, c.HoldoutDeviation*100)
			}
			fmt.Println()
		}
	case "align":
		results, b, err := runner.RunAlign(ctx)
		if err != nil {
			return err
		}
		budgets = b
		for _, r := range results {
			if r.Err != nil {
				failed++
			}
		}
	case "voice-smoke":
		report, err := runner.RunVoiceSmoke(ctx, ollamaModel)
		if err != nil {
			return err
		}
		failed, budgets = countFailed(report.Voices), report.Budgets
		if report.Align.Err != nil {
			failed++
		}
	case "vision":
		results, b, err := runner.RunVision(ctx, refs)
		if err != nil {
			return err
		}
		budgets = b
		for _, r := range results {
			if r.Err != nil {
				failed++
			}
		}
	case "train", "train-smoke":
		run := runner.RunTrain
		if suite == "train-smoke" {
			run = runner.RunTrainSmoke
		}
		report, err := run(ctx, refs)
		if err != nil {
			return err
		}
		budgets = report.Budgets
		if report.Train.Err != nil {
			failed++
		}
		if report.Score != nil && report.Score.Err != nil {
			failed++
		}
	}
	return finishBudgets(budgets, failed)
}

func runLLMSuite(ctx context.Context, manager *residency.Manager, store *models.Store, outDir, ollamaURL, ollamaModel string, logf func(string, ...any)) error {
	var targets []bench.LLMTarget
	if ollamaURL != "" && ollamaModel != "" {
		targets = append(targets, bench.LLMTarget{
			Name: ollamaModel, Provider: ollama.New(ollamaURL, ollamaModel, benchHTTPClient()),
			Residency: &pipeline.ModelRef{Backend: "ollama", Model: ollamaModel},
		})
	}
	if url, token := os.Getenv("LLMCLI_URL"), readSecretFile(os.Getenv("LLMCLI_BEARER_TOKEN_PATH")); url != "" && token != "" {
		targets = append(targets, bench.LLMTarget{Name: "claude-cli", Provider: claudecli.New(url, secretstr.String(token), benchHTTPClient(), false)})
	}
	if len(targets) == 0 {
		return errors.New("bench llm: set OLLAMA_URL with -ollama-model, and/or LLMCLI_URL with LLMCLI_BEARER_TOKEN_PATH")
	}
	runner := &bench.LLMRunner{Residency: manager, Queries: store.Queries, OutDir: outDir, Log: logf}
	results, budgets, err := runner.Run(ctx, targets)
	if err != nil {
		return err
	}
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
		}
	}
	if outDir != "" {
		fmt.Printf("outputs and ratings.csv (rate each 1-5 by hand) are in %s/llm\n", outDir)
	}
	return finishBudgets(budgets, failed)
}

func countFailed(results []bench.VoiceResult) int {
	n := 0
	for _, r := range results {
		if r.Err != nil {
			n++
		}
	}
	return n
}

// finishBudgets prints the budget checks; the command fails when any
// case failed (budgets are reported, not enforced).
func finishBudgets(budgets []bench.Budget, failed int) error {
	fmt.Println("\nperformance budgets:")
	for _, b := range budgets {
		fmt.Println("  " + b.String())
	}
	if failed > 0 {
		return fmt.Errorf("%d case(s) failed", failed)
	}
	return nil
}

func readSecretFile(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
