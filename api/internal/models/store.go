package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// Install status values, matching the model_installs.status CHECK.
// "not_installed" and "blocked" are never stored: they are what the API
// reports for an entry with no row and for a refused licence.
const (
	StatusNotInstalled = "not_installed"
	StatusDownloading  = "downloading"
	StatusPaused       = "paused"
	StatusInstalled    = "installed"
	StatusFailed       = "failed"
	StatusBlocked      = "blocked"
)

// ErrAlreadyDownloading is returned by Claim when a pull is in flight.
var ErrAlreadyDownloading = errors.New("models: a download for this model is already running")

// Store is the database side of model installs and verified files.
type Store struct {
	Queries dbgen.Querier
}

var _ FileStore = (*Store)(nil)

// VerifiedFile implements FileStore.
func (s *Store) VerifiedFile(ctx context.Context, path string) (string, int64, bool, error) {
	files, err := s.Queries.ListModelFiles(ctx)
	if err != nil {
		return "", 0, false, err
	}
	for _, f := range files {
		if f.Path == path {
			return f.Digest, f.SizeBytes, true, nil
		}
	}
	return "", 0, false, nil
}

// MarkFileVerified implements FileStore.
func (s *Store) MarkFileVerified(ctx context.Context, path, sha string, size int64) error {
	return s.Queries.UpsertModelFile(ctx, dbgen.UpsertModelFileParams{Path: path, Digest: sha, SizeBytes: size})
}

// Claim moves e to "downloading" for tenantID, or returns
// ErrAlreadyDownloading.
func (s *Store) Claim(ctx context.Context, e Entry, tenantID uuid.UUID, alreadyDone int64) (dbgen.ModelInstall, error) {
	// uuid.Nil (the operator CLI, which acts outside any tenant) is
	// stored as NULL.
	var tenantPg *uuid.UUID
	if tenantID != uuid.Nil {
		tenantPg = &tenantID
	}
	row, err := s.Queries.ClaimModelInstall(ctx, dbgen.ClaimModelInstallParams{
		Name:              e.Name,
		BytesDone:         alreadyDone,
		BytesTotal:        e.SizeBytes(),
		StartedByTenantID: idconv.ToPgPtr(tenantPg),
		LicenceSpdx:       e.Licence.SPDX,
		LicenceUrl:        e.Licence.URL,
		Revision:          e.Source.Revision,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.ModelInstall{}, ErrAlreadyDownloading
	}
	return row, err
}

// MarkInstalled records e as installed with every byte verified.
func (s *Store) MarkInstalled(ctx context.Context, e Entry) error {
	return s.Queries.MarkModelInstalled(ctx, dbgen.MarkModelInstalledParams{
		Name:        e.Name,
		BytesTotal:  e.SizeBytes(),
		LicenceSpdx: e.Licence.SPDX,
		LicenceUrl:  e.Licence.URL,
		Revision:    e.Source.Revision,
	})
}

// VerifiedBytes sums the sizes of e's files already verified on disk.
func (s *Store) VerifiedBytes(ctx context.Context, e Entry) (int64, error) {
	files, err := s.Queries.ListModelFiles(ctx)
	if err != nil {
		return 0, err
	}
	verified := map[string]dbgen.ModelFile{}
	for _, f := range files {
		verified[f.Path] = f
	}
	var total int64
	for _, f := range e.Files {
		if v, ok := verified[f.Path]; ok && v.Digest == f.Digest() && v.SizeBytes == f.Size {
			total += f.Size
		}
	}
	return total, nil
}

// LoadGate re-runs the licence gate and checks every file of the model
// is verified in the database and present on disk with its pinned size.
// Engines call it before loading so a model is never loaded from files
// the app has not verified.
type LoadGate struct {
	Manifest *Manifest
	Store    *Store
	// Dir is the models volume root as mounted in this process.
	Dir string
}

// Check implements the gate for model name.
func (g *LoadGate) Check(ctx context.Context, name string) error {
	e, ok := g.Manifest.Get(name)
	if !ok {
		return fmt.Errorf("%w: %q is not in the manifest", ErrNotInstalled, name)
	}
	if err := Gate(e); err != nil {
		return err
	}
	files, err := g.Store.Queries.ListModelFiles(ctx)
	if err != nil {
		return err
	}
	verified := map[string]dbgen.ModelFile{}
	for _, f := range files {
		verified[f.Path] = f
	}
	for _, f := range e.Files {
		v, ok := verified[f.Path]
		if !ok || v.Digest != f.Digest() || v.SizeBytes != f.Size {
			return fmt.Errorf("%w: %s is not verified (install the model first)", ErrNotInstalled, f.Path)
		}
		info, err := os.Stat(filepath.Join(g.Dir, filepath.FromSlash(f.Path)))
		if err != nil || info.Size() != f.Size {
			return fmt.Errorf("%w: %s is missing or has the wrong size on disk", ErrNotInstalled, f.Path)
		}
	}
	return nil
}

// Remove deletes e's files that no other installed model still uses,
// forgets their verification and drops e's install row.
func (s *Store) Remove(ctx context.Context, m *Manifest, e Entry, dir string) error {
	installs, err := s.Queries.ListModelInstalls(ctx)
	if err != nil {
		return err
	}
	shared := map[string]bool{}
	for _, row := range installs {
		if row.Name == e.Name || row.Status != StatusInstalled {
			continue
		}
		if other, ok := m.Get(row.Name); ok {
			for _, f := range other.Files {
				shared[f.Path] = true
			}
		}
	}
	for _, f := range e.Files {
		if shared[f.Path] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(f.Path))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := s.Queries.DeleteModelFile(ctx, f.Path); err != nil {
			return err
		}
	}
	return s.Queries.DeleteModelInstall(ctx, e.Name)
}
