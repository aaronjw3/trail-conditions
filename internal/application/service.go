package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/aaronjw3/trail-conditions/internal/domain"
)

// Contracts are owned by the workflow; adapters depend on these domain types.
type RouteDecoder interface {
	Decode(filename string, data []byte) (domain.Route, error)
}
type Weather interface {
	Forecast(context.Context, domain.Point) (domain.Forecast, error)
}
type SnapshotStore interface {
	Load(context.Context) (*domain.Snapshot, error)
	Save(context.Context, *domain.Snapshot) error
}

var ErrWeatherUnavailable = errors.New("weather service unavailable")

var ErrBusy = errors.New("a forecast request is already running")

type InputError struct{ Err error }

func (e *InputError) Error() string { return e.Err.Error() }
func (e *InputError) Unwrap() error { return e.Err }

type Service struct {
	decoder  RouteDecoder
	weather  Weather
	store    SnapshotStore
	refresh  sync.Mutex
	mu       sync.RWMutex
	snapshot *domain.Snapshot
}

func New(ctx context.Context, decoder RouteDecoder, weather Weather, store SnapshotStore) (*Service, error) {
	if decoder == nil || weather == nil || store == nil {
		return nil, fmt.Errorf("decoder, weather provider, and snapshot store are required")
	}
	snapshot, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	if snapshot != nil {
		if err := snapshot.Validate(); err != nil {
			return nil, err
		}
	}
	return &Service{decoder: decoder, weather: weather, store: store, snapshot: snapshot}, nil
}

// Snapshot returns a copy so callers cannot mutate a published result.
func (s *Service) Snapshot() *domain.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapshot == nil {
		return nil
	}
	copy := *s.snapshot
	copy.Route.Coordinates = make([][]domain.Point, len(s.snapshot.Route.Coordinates))
	for i, segment := range s.snapshot.Route.Coordinates {
		copy.Route.Coordinates[i] = slices.Clone(segment)
	}
	copy.Forecasts = slices.Clone(s.snapshot.Forecasts)
	for i, f := range s.snapshot.Forecasts {
		copy.Forecasts[i].Times = slices.Clone(f.Times)
		copy.Forecasts[i].Temperature = slices.Clone(f.Temperature)
		copy.Forecasts[i].Precipitation = slices.Clone(f.Precipitation)
		copy.Forecasts[i].Wind = slices.Clone(f.Wind)
	}
	return &copy
}
func (s *Service) Import(ctx context.Context, filename string, data []byte) error {
	if !s.refresh.TryLock() {
		return ErrBusy
	}
	defer s.refresh.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	route, err := s.decoder.Decode(filename, data)
	if err != nil {
		return &InputError{err}
	}
	return s.collect(ctx, route)
}
func (s *Service) Refresh(ctx context.Context) error {
	if !s.refresh.TryLock() {
		return ErrBusy
	}
	defer s.refresh.Unlock()
	previous := s.Snapshot()
	if previous == nil {
		return &InputError{fmt.Errorf("upload a route first")}
	}
	return s.collect(ctx, previous.Route)
}
func (s *Service) collect(ctx context.Context, route domain.Route) error {
	points, err := route.Samples()
	if err != nil {
		return &InputError{err}
	}
	next := &domain.Snapshot{Route: route}
	for _, point := range points {
		if err := ctx.Err(); err != nil {
			return err
		}
		forecast, err := s.weather.Forecast(ctx, point)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrWeatherUnavailable, err)
		}
		next.Forecasts = append(next.Forecasts, forecast)
	}
	next.RetrievedAt = time.Now().UTC().Truncate(time.Microsecond)
	if err := next.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.store.Save(ctx, next); err != nil {
		return err
	}
	s.mu.Lock()
	s.snapshot = next
	s.mu.Unlock()
	return nil
}
