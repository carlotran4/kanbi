package app

import (
	"context"
	"errors"

	"github.com/carlotran4/kanbi/internal/multiplexer"
)

type FleetManager interface {
	PreviewFleet(context.Context, int64, int, int) (multiplexer.FleetSummary, error)
	EnterFleet(context.Context, int64, int, int, string) error
	RestoreFleet(context.Context) error
	ResizeFleet(context.Context) error
	FleetStatus(context.Context) (multiplexer.FleetStatus, error)
}

func (s *Service) fleetManager() (FleetManager, error) {
	m, ok := s.Manager.(FleetManager)
	if !ok {
		return nil, errors.New("Fleet is unavailable for this runtime")
	}
	return m, nil
}
func (s *Service) PreviewFleet(ctx context.Context, b int64, w, h int) (multiplexer.FleetSummary, error) {
	m, e := s.fleetManager()
	if e != nil {
		return multiplexer.FleetSummary{}, e
	}
	return m.PreviewFleet(ctx, b, w, h)
}
func (s *Service) EnterFleet(ctx context.Context, b int64, w, h int, view string) error {
	m, e := s.fleetManager()
	if e != nil {
		return e
	}
	return m.EnterFleet(ctx, b, w, h, view)
}
func (s *Service) RestoreFleet(ctx context.Context) error {
	m, e := s.fleetManager()
	if e != nil {
		return e
	}
	return m.RestoreFleet(ctx)
}
func (s *Service) ResizeFleet(ctx context.Context) error {
	m, e := s.fleetManager()
	if e != nil {
		return e
	}
	return m.ResizeFleet(ctx)
}
func (s *Service) FleetStatus(ctx context.Context) (multiplexer.FleetStatus, error) {
	m, e := s.fleetManager()
	if e != nil {
		return multiplexer.FleetStatus{}, nil
	}
	return m.FleetStatus(ctx)
}
