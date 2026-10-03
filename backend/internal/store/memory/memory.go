// Package memory 提供 Store 的进程内实现，用于单元测试与无数据库演示。
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"essplanner/internal/core"
	"essplanner/internal/store"
)

type Store struct {
	mu         sync.Mutex
	station    *core.StationParams
	prices     map[string][]float64
	plans      map[string][]core.Plan // date -> 按版本升序
	telemetry  map[string]core.Telemetry
	nextPlanID int64
}

func New() *Store {
	return &Store{
		prices:    make(map[string][]float64),
		plans:     make(map[string][]core.Plan),
		telemetry: make(map[string]core.Telemetry),
	}
}

func (s *Store) Close() {}

func (s *Store) GetStation(_ context.Context) (*core.StationParams, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.station == nil {
		return nil, store.ErrNotFound
	}
	p := *s.station
	return &p, nil
}

func (s *Store) PutStation(_ context.Context, p *core.StationParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *p
	s.station = &cp
	return nil
}

func (s *Store) GetPrices(_ context.Context, date string) ([]float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr, ok := s.prices[date]
	if !ok {
		return nil, store.ErrNotFound
	}
	return append([]float64(nil), pr...), nil
}

func (s *Store) PutPrices(_ context.Context, date string, prices []float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prices[date] = append([]float64(nil), prices...)
	return nil
}

func (s *Store) CreatePlan(_ context.Context, plan *core.Plan) (*core.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextPlanID++
	cp := *plan
	cp.ID = s.nextPlanID
	cp.Version = len(s.plans[plan.Date]) + 1
	cp.CreatedAt = time.Now()
	cp.Slots = append([]core.SlotPlan(nil), plan.Slots...)
	s.plans[plan.Date] = append(s.plans[plan.Date], cp)
	return &cp, nil
}

func (s *Store) GetCurrentPlan(_ context.Context, date string) (*core.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.plans[date]
	if len(versions) == 0 {
		return nil, store.ErrNotFound
	}
	p := versions[len(versions)-1]
	p.Slots = append([]core.SlotPlan(nil), p.Slots...)
	return &p, nil
}

func (s *Store) GetPlanVersion(_ context.Context, date string, version int) (*core.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.plans[date] {
		if p.Version == version {
			p.Slots = append([]core.SlotPlan(nil), p.Slots...)
			return &p, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *Store) ListPlanVersions(_ context.Context, date string) ([]core.PlanMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.plans[date]
	metas := make([]core.PlanMeta, 0, len(versions))
	for i := len(versions) - 1; i >= 0; i-- { // 新版本在前
		p := versions[i]
		metas = append(metas, core.PlanMeta{
			ID: p.ID, Date: p.Date, Version: p.Version,
			TriggerReason: p.TriggerReason, ExpectedRevenue: p.ExpectedRevenue,
			InitialSocMWh: p.InitialSocMWh, BasedOnTs: p.BasedOnTs, CreatedAt: p.CreatedAt,
		})
	}
	return metas, nil
}

func (s *Store) InsertTelemetry(_ context.Context, t *core.Telemetry) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.telemetry[t.ID]; dup {
		return false, nil
	}
	s.telemetry[t.ID] = *t
	return true, nil
}

func (s *Store) LatestTelemetry(_ context.Context, date string) (*core.Telemetry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest *core.Telemetry
	for _, tm := range s.telemetry {
		if tm.Date != date {
			continue
		}
		if latest == nil || tm.Ts.After(latest.Ts) {
			cp := tm
			latest = &cp
		}
	}
	if latest == nil {
		return nil, store.ErrNotFound
	}
	return latest, nil
}

func (s *Store) ListTelemetry(_ context.Context, date string) ([]core.Telemetry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []core.Telemetry
	for _, tm := range s.telemetry {
		if tm.Date == date {
			out = append(out, tm)
		}
	}
	// 按采集时刻归位，晚到的旧数据插入正确位置
	sort.Slice(out, func(i, j int) bool { return out[i].Ts.Before(out[j].Ts) })
	return out, nil
}
