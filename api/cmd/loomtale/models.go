package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/models"
	"loomtale/api/internal/providers/image/comfyui"
)

// runModels dispatches `loomtale models <lint|list|pull|remove>`.
//
//	loomtale models lint [-manifest path] [-workflows dir]
//	loomtale models list
//	loomtale models pull <name>
//	loomtale models remove <name>
//
// pull and remove need DATABASE_URL and MODELS_DIR (the models volume,
// mounted read-write): run them through the `cli` compose service in
// deploy/compose.gpu.yml, which is the only place besides the worker
// that mounts it writable.
func runModels(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: loomtale models <lint|list|pull|remove> [args]")
	}
	switch args[0] {
	case "lint":
		return runModelsLint(args[1:])
	case "list":
		return runModelsList(ctx)
	case "pull":
		return runModelsPull(ctx, args[1:])
	case "remove":
		return runModelsRemove(ctx, args[1:])
	default:
		return fmt.Errorf("unknown models subcommand %q", args[0])
	}
}

// runModelsLint lints the source manifest and workflow templates (not
// the embedded copies), so `make lint` catches a bad edit before `make
// gen` copies it into the binaries.
func runModelsLint(args []string) error {
	fs := flag.NewFlagSet("models lint", flag.ExitOnError)
	manifestPath := fs.String("manifest", "models/manifest.yaml", "manifest to lint")
	workflowsDir := fs.String("workflows", "comfyui/workflows", "workflow template directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	m, err := models.Parse(raw)
	if err != nil {
		return err
	}
	templates, err := comfyui.LoadTemplates(os.DirFS(*workflowsDir), ".")
	if err != nil {
		return err
	}
	problems := models.Lint(m, templates)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "manifest:", p)
	}
	if len(problems) > 0 {
		return fmt.Errorf("manifest lint failed with %d problem(s)", len(problems))
	}
	fmt.Printf("manifest lint: OK (%d models, %d workflows)\n", len(m.Models), len(templates))
	return nil
}

func runModelsList(ctx context.Context) error {
	m, err := models.Embedded()
	if err != nil {
		return err
	}
	installs := map[string]gen.ModelInstall{}
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		defer pool.Close()
		rows, err := gen.New(pool).ListModelInstalls(ctx)
		if err != nil {
			// The manifest view is still useful without install state.
			fmt.Fprintln(os.Stderr, "warning: install state unavailable:", err)
		}
		for _, r := range rows {
			installs[r.Name] = r
		}
	}
	// tabwriter buffers rows; a write error surfaces from Flush.
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tTASK\tLICENCE\tSIZE\tVRAM\tSTATUS")
	for _, e := range m.Models {
		status := models.StatusNotInstalled
		if !models.LicenceAllowed(e.Licence.SPDX) {
			status = models.StatusBlocked
		} else if r, ok := installs[e.Name]; ok {
			status = r.Status
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%.1f GB\t%d MB\t%s\n", e.Name, e.Task, e.Licence.SPDX, float64(e.SizeBytes())/1e9, e.VRAMMB, status)
	}
	return w.Flush()
}

func runModelsPull(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: loomtale models pull <name>")
	}
	m, store, dir, closeDB, err := modelsEnv(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	e, ok := m.Get(args[0])
	if !ok {
		return fmt.Errorf("no model %q in the manifest (try: loomtale models list)", args[0])
	}
	if err := models.Gate(e); err != nil {
		return err
	}
	verified, err := store.VerifiedBytes(ctx, e)
	if err != nil {
		return err
	}
	if _, err := store.Claim(ctx, e, uuid.Nil, verified); err != nil {
		return err
	}

	downloader := &models.Downloader{
		Dir: dir, HostDiskDir: strings.TrimSpace(os.Getenv("MODELS_HOST_DISK_DIR")),
		HTTP: cliDownloadClient(), Files: store,
	}
	last := time.Time{}
	err = downloader.Install(ctx, e, func(done, total int64) {
		if time.Since(last) < 5*time.Second && done < total {
			return
		}
		last = time.Now()
		fmt.Printf("%s: %.2f / %.2f GB (%d%%)\n", e.Name, float64(done)/1e9, float64(total)/1e9, done*100/max(total, 1))
		_ = store.Queries.UpdateModelInstallProgress(ctx, gen.UpdateModelInstallProgressParams{Name: e.Name, BytesDone: done, BytesTotal: total})
	})
	if err != nil {
		failCtx := context.WithoutCancel(ctx)
		_ = store.Queries.MarkModelInstallFailed(failCtx, gen.MarkModelInstallFailedParams{Name: e.Name, Error: idconv.ToPgText(err.Error())})
		return err
	}
	if err := store.MarkInstalled(ctx, e); err != nil {
		return err
	}
	fmt.Printf("%s: installed and verified (%d files)\n", e.Name, len(e.Files))
	return nil
}

func runModelsRemove(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: loomtale models remove <name>")
	}
	m, store, dir, closeDB, err := modelsEnv(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	e, ok := m.Get(args[0])
	if !ok {
		return fmt.Errorf("no model %q in the manifest", args[0])
	}
	if err := store.Remove(ctx, m, e, dir); err != nil {
		return err
	}
	fmt.Printf("%s: removed (files shared with another installed model were kept)\n", e.Name)
	return nil
}

// modelsEnv loads the embedded manifest, connects to DATABASE_URL and
// checks MODELS_DIR.
func modelsEnv(ctx context.Context) (*models.Manifest, *models.Store, string, func(), error) {
	m, err := models.Embedded()
	if err != nil {
		return nil, nil, "", nil, err
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, nil, "", nil, errRequiredEnv("DATABASE_URL")
	}
	dir := strings.TrimSpace(os.Getenv("MODELS_DIR"))
	if dir == "" {
		return nil, nil, "", nil, errRequiredEnv("MODELS_DIR")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, "", nil, err
	}
	return m, &models.Store{Queries: gen.New(pool)}, dir, pool.Close, nil
}

func cliDownloadClient() *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}}
}
