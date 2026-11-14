package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aaronjw3/trail-conditions/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

type Store struct{ pool *pgxpool.Pool }

// New creates the singleton table before the application starts serving requests.
// The caller owns the pool and closes it during shutdown.
func New(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	if _, err := pool.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("initialize snapshot table: %w", err)
	}
	return &Store{pool: pool}, nil
}
func (s *Store) Save(ctx context.Context, snapshot *domain.Snapshot) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot is required")
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	route, err := json.Marshal(snapshot.Route)
	if err != nil {
		return err
	}
	forecasts, err := json.Marshal(snapshot.Forecasts)
	if err != nil {
		return err
	}
	// One upsert is one PostgreSQL transaction: readers cannot see half a snapshot.
	_, err = s.pool.Exec(ctx, `INSERT INTO current_snapshot (singleton,retrieved_at,route,forecasts)
 VALUES (true,$1,$2::jsonb,$3::jsonb)
 ON CONFLICT (singleton) DO UPDATE SET retrieved_at=EXCLUDED.retrieved_at,
 route=EXCLUDED.route,forecasts=EXCLUDED.forecasts`, snapshot.RetrievedAt, route, forecasts)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}
func (s *Store) Load(ctx context.Context) (*domain.Snapshot, error) {
	var snapshot domain.Snapshot
	var route, forecasts []byte
	err := s.pool.QueryRow(ctx, `SELECT retrieved_at,route,forecasts FROM current_snapshot WHERE singleton=true`).Scan(&snapshot.RetrievedAt, &route, &forecasts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	snapshot.RetrievedAt = snapshot.RetrievedAt.UTC()
	if err = json.Unmarshal(route, &snapshot.Route); err != nil {
		return nil, fmt.Errorf("decode route: %w", err)
	}
	if err = json.Unmarshal(forecasts, &snapshot.Forecasts); err != nil {
		return nil, fmt.Errorf("decode forecasts: %w", err)
	}
	if err = snapshot.Validate(); err != nil {
		return nil, fmt.Errorf("stored snapshot: %w", err)
	}
	return &snapshot, nil
}
