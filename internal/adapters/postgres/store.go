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

// New initializes storage and migrates the previous single-snapshot table.
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
	_, err = s.pool.Exec(ctx, `INSERT INTO route_snapshots (id,name,retrieved_at,route,forecasts)
 VALUES ($1,$2,$3,$4::jsonb,$5::jsonb)
 ON CONFLICT (id) DO UPDATE SET retrieved_at=EXCLUDED.retrieved_at,
 route=EXCLUDED.route,forecasts=EXCLUDED.forecasts`, snapshot.ID, snapshot.Name, snapshot.RetrievedAt, route, forecasts)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}
func (s *Store) Load(ctx context.Context, id string) (*domain.Snapshot, error) {
	var snapshot domain.Snapshot
	var route, forecasts []byte
	err := s.pool.QueryRow(ctx, `SELECT id,name,retrieved_at,route,forecasts FROM route_snapshots WHERE id=$1`, id).Scan(&snapshot.ID, &snapshot.Name, &snapshot.RetrievedAt, &route, &forecasts)
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

func (s *Store) List(ctx context.Context) ([]domain.SavedRoute, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,retrieved_at FROM route_snapshots ORDER BY retrieved_at DESC,id`)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}
	defer rows.Close()
	routes := []domain.SavedRoute{}
	for rows.Next() {
		var route domain.SavedRoute
		if err := rows.Scan(&route.ID, &route.Name, &route.RetrievedAt); err != nil {
			return nil, err
		}
		route.RetrievedAt = route.RetrievedAt.UTC()
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func (s *Store) LoadForecasts(ctx context.Context, id string) (*domain.ForecastSnapshot, error) {
	var snapshot domain.ForecastSnapshot
	var forecasts []byte
	err := s.pool.QueryRow(ctx, `SELECT id,name,retrieved_at,forecasts FROM route_snapshots WHERE id=$1`, id).Scan(&snapshot.ID, &snapshot.Name, &snapshot.RetrievedAt, &forecasts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load forecasts: %w", err)
	}
	snapshot.RetrievedAt = snapshot.RetrievedAt.UTC()
	if err = json.Unmarshal(forecasts, &snapshot.Forecasts); err != nil {
		return nil, fmt.Errorf("decode forecasts: %w", err)
	}
	if err = snapshot.Validate(); err != nil {
		return nil, fmt.Errorf("stored forecasts: %w", err)
	}
	return &snapshot, nil
}
