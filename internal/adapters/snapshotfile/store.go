package snapshotfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aaronjw3/trail-conditions/internal/domain"
)

type Store struct{ Path string }

func (s Store) Save(ctx context.Context, snapshot *domain.Snapshot) error {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	// Persist the whole result before publishing it to readers. Rename is atomic
	// because the temporary file lives in the same directory as the destination.
	f, err := os.CreateTemp(filepath.Dir(s.Path), ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(encoded); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.Path); err != nil {
		return err
	}
	return nil
}
func (s Store) Load(ctx context.Context) (*domain.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 8<<20 {
		return nil, fmt.Errorf("stored snapshot too large")
	}
	var snapshot domain.Snapshot
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	if err = snapshot.Validate(); err != nil {
		return nil, fmt.Errorf("stored snapshot: %w", err)
	}
	return &snapshot, nil
}
