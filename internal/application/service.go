package application

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/aaronjw3/trail-conditions/internal/domain"
	"golang.org/x/sync/errgroup"
)

type RouteDecoder interface {
	Decode(string, []byte) (domain.Route, error)
}
type Weather interface {
	Forecast(context.Context, domain.Point) (domain.Forecast, error)
}
type SnapshotStore interface {
	Load(context.Context, string) (*domain.Snapshot, error)
	LoadForecasts(context.Context, string) (*domain.ForecastSnapshot, error)
	List(context.Context) ([]domain.SavedRoute, error)
	Save(context.Context, *domain.Snapshot) error
}

var (
	ErrWeatherUnavailable = errors.New("weather service unavailable")
	ErrBusy               = errors.New("this route is already being refreshed")
	ErrCapacity           = errors.New("four uploads or refreshes are already running; try again shortly")
	ErrNotFound           = errors.New("route not found")
)

type InputError struct{ Err error }

func (e *InputError) Error() string { return e.Err.Error() }
func (e *InputError) Unwrap() error { return e.Err }

type Service struct {
	decoder      RouteDecoder
	weather      Weather
	store        SnapshotStore
	operations   chan struct{}
	weatherSlots chan struct{}
	refreshing   sync.Map
}

func New(decoder RouteDecoder, weather Weather, store SnapshotStore) (*Service, error) {
	if decoder == nil || weather == nil || store == nil {
		return nil, fmt.Errorf("decoder, weather provider, and snapshot store are required")
	}
	return &Service{
		decoder:      decoder,
		weather:      weather,
		store:        store,
		operations:   make(chan struct{}, 4),
		weatherSlots: make(chan struct{}, 3),
	}, nil
}

func (s *Service) Snapshot(ctx context.Context, id string) (*domain.Snapshot, error) {
	snapshot, err := s.store.Load(ctx, id)
	if err == nil && snapshot == nil {
		return nil, ErrNotFound
	}
	return snapshot, err
}
func (s *Service) Forecasts(ctx context.Context, id string) (*domain.ForecastSnapshot, error) {
	snapshot, err := s.store.LoadForecasts(ctx, id)
	if err == nil && snapshot == nil {
		return nil, ErrNotFound
	}
	return snapshot, err
}

func (s *Service) Routes(ctx context.Context) ([]domain.SavedRoute, error) { return s.store.List(ctx) }

func (s *Service) Import(ctx context.Context, filename string, data []byte) (*domain.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.operations <- struct{}{}:
		defer func() { <-s.operations }()
	default:
		return nil, ErrCapacity
	}
	route, err := s.decoder.Decode(filename, data)
	if err != nil {
		return nil, &InputError{err}
	}
	next := &domain.Snapshot{ForecastSnapshot: domain.ForecastSnapshot{ID: rand.Text(), Name: filepath.Base(filename)}, Route: route}
	return s.collect(ctx, next)
}

func (s *Service) Refresh(ctx context.Context, id string) (*domain.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, loaded := s.refreshing.LoadOrStore(id, struct{}{}); loaded {
		return nil, ErrBusy
	}
	defer s.refreshing.Delete(id)
	select {
	case s.operations <- struct{}{}:
		defer func() { <-s.operations }()
	default:
		return nil, ErrCapacity
	}
	previous, err := s.Snapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.collect(ctx, &domain.Snapshot{ForecastSnapshot: domain.ForecastSnapshot{ID: id, Name: previous.Name}, Route: previous.Route})
}

func (s *Service) collect(ctx context.Context, next *domain.Snapshot) (*domain.Snapshot, error) {
	points, err := next.Route.Samples()
	if err != nil {
		return nil, &InputError{err}
	}
	next.Forecasts = make([]domain.Forecast, len(points))
	group, requestCtx := errgroup.WithContext(ctx)
	for i, point := range points {
		group.Go(func() error {
			select {
			case s.weatherSlots <- struct{}{}:
				defer func() { <-s.weatherSlots }()
			case <-requestCtx.Done():
				return requestCtx.Err()
			}
			if err := requestCtx.Err(); err != nil {
				return err
			}
			forecast, err := s.weather.Forecast(requestCtx, point)
			if err != nil {
				return fmt.Errorf("%w: %w", ErrWeatherUnavailable, err)
			}
			// Each goroutine owns one slot, preserving route order regardless of completion order.
			next.Forecasts[i] = forecast
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	next.RetrievedAt = time.Now().UTC().Truncate(time.Microsecond)
	if err := next.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.store.Save(ctx, next); err != nil {
		return nil, err
	}
	return next, nil
}
