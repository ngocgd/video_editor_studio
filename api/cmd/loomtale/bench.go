package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"loomtale/api/internal/bench"
	"loomtale/api/internal/models"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/image/comfyui"
	"loomtale/api/internal/providers/residency"
)

// runBench runs a benchmark suite against the real engines:
//
//	loomtale bench --suite image-smoke [--out /bench] [--rss-samples /bench/rss.tsv]
//	loomtale bench --suite tts|align|llm|voice-smoke [--out /bench] [--ollama-model qwen3.5-9b]
//
// The image suites need DATABASE_URL, MODELS_DIR and COMFYUI_URL
// (scripts/bench-image.sh wraps them with the host-side RSS sampler);
// the voice and llm suites are described at runVoiceBench. Run it
// through the `cli` compose service. It takes the GPU slot's advisory
// lock for its whole run, so no gpu-queue step touches the GPU meanwhile.
func runBench(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	suiteName := fs.String("suite", "", "suite to run: "+suiteNames())
	outDir := fs.String("out", "", "directory to save output images into (optional)")
	rssSamples := fs.String("rss-samples", "", "file of '<unix_ms> <rss_bytes>' ComfyUI RSS samples written by the host (optional)")
	ollamaModel := fs.String("ollama-model", os.Getenv("OLLAMA_MODEL"), "manifest LLM to load in Ollama for the llm and voice-smoke suites")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if isVoiceSuite(*suiteName) {
		return runVoiceBench(ctx, *suiteName, *outDir, *ollamaModel)
	}
	suite, ok := bench.Suites()[*suiteName]
	if !ok {
		return fmt.Errorf("unknown suite %q (have: %s)", *suiteName, suiteNames())
	}

	m, store, dir, closeDB, err := modelsEnv(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	comfyURL := os.Getenv("COMFYUI_URL")
	if comfyURL == "" {
		return errRequiredEnv("COMFYUI_URL")
	}
	reserveMB := int64(1024)
	if v := os.Getenv("RENDER_RESERVE_MB"); v != "" {
		if reserveMB, err = strconv.ParseInt(v, 10, 64); err != nil {
			return fmt.Errorf("RENDER_RESERVE_MB: %w", err)
		}
	}

	unlock, err := lockGPUSlot(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer unlock()

	templates, err := models.EmbeddedTemplates()
	if err != nil {
		return err
	}
	engine := &comfyui.Engine{Client: comfyui.New(comfyURL, benchHTTPClient()), Templates: templates}
	gate := &models.LoadGate{Manifest: m, Store: store, Dir: dir}
	backend := &comfyui.Backend{Engine: engine, Warmups: m.Warmups(), Gate: gate.Check}
	probe := &comfyui.GpuProbe{Client: engine.Client, RenderReserveMB: reserveMB}
	manager, err := residency.NewManagerWithBudget(ctx, probe, map[string]residency.Backend{"comfyui": backend}, m.VRAMByRef(), reserveMB)
	if err != nil {
		return err
	}
	defer func() { _ = manager.UnloadAll(context.WithoutCancel(ctx)) }()
	fmt.Printf("bench %s: %d cases, VRAM budget %d MB (free at start minus %d MB render reserve)\n", suite.Name, len(suite.Cases), manager.BudgetMB, reserveMB)

	h := &bench.Harness{
		Engine: engine, Residency: manager, Queries: store.Queries, OutDir: *outDir,
		SwapUsedMB: bench.SwapUsedMB,
		Log:        func(format string, args ...any) { fmt.Printf(format+"\n", args...) },
	}
	if *rssSamples != "" {
		h.RSS = bench.RSSFromSamplesFile(*rssSamples)
	}
	if *outDir != "" {
		if err := os.MkdirAll(*outDir, 0o755); err != nil {
			return err
		}
	}
	report, err := h.Run(ctx, suite)
	if err != nil {
		return err
	}
	return printReport(report)
}

func printReport(r bench.Report) error {
	fmt.Printf("\nrun %s suite %s\n", r.RunID, r.Suite.Name)
	failed := 0
	var rssPeak int64
	for _, res := range r.Results {
		if !res.OK {
			failed++
		}
		rssPeak = max(rssPeak, res.RSSPeakMB)
	}
	fmt.Printf("cases %d, failed %d, ComfyUI RSS peak %d MB", len(r.Results), failed, rssPeak)
	if r.HasSwap {
		fmt.Printf(", VM swap peak %d MB", r.SwapPeakMB)
	}
	fmt.Println()
	if r.Suite.Gate {
		if r.GateOK {
			fmt.Println("go/no-go gate: GO")
		} else {
			fmt.Println("go/no-go gate: NO-GO")
			for _, reason := range r.GateReasons {
				fmt.Println("  -", reason)
			}
			return errors.New("go/no-go gate failed")
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d case(s) failed", failed)
	}
	return nil
}

// lockGPUSlot takes the same session advisory lock the GPU executor
// holds while a gpu-queue step runs, on a dedicated connection.
func lockGPUSlot(ctx context.Context, dsn string) (func(), error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		return nil, err
	}
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", pipeline.GPUSlotLockKey).Scan(&locked); err != nil {
		conn.Release()
		pool.Close()
		return nil, err
	}
	if !locked {
		conn.Release()
		pool.Close()
		return nil, errors.New("the GPU slot is busy (a gpu-queue step is running); retry when it is idle")
	}
	return func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", pipeline.GPUSlotLockKey)
		conn.Release()
		pool.Close()
	}, nil
}

func benchHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext, ResponseHeaderTimeout: 60 * time.Second}}
}

func suiteNames() string {
	names := append([]string(nil), voiceSuites...)
	for name := range bench.Suites() {
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Sprint(names)
}
