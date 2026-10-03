package service

import (
	"context"
	"sort"
	"sync"
)

// MemoryStore 是 Store 的内存实现，供单元测试使用。
type MemoryStore struct {
	mu      sync.Mutex
	config  *Config
	plans   map[string][]*PlanVersion
	tele    map[string]map[string]Telemetry
	current map[string]int // planDay -> version
}

// NewMemoryStore 构造空内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		plans:   map[string][]*PlanVersion{},
		tele:    map[string]map[string]Telemetry{},
		current: map[string]int{},
	}
}

// LoadConfig 实现 Store。
func (m *MemoryStore) LoadConfig(context.Context) (*Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.config == nil {
		return nil, &ErrNotFound{What: "配置"}
	}
	c := *m.config
	c.Prices = append([]float64(nil), m.config.Prices...)
	return &c, nil
}

// SaveConfig 实现 Store。
func (m *MemoryStore) SaveConfig(_ context.Context, cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := cfg
	c.Prices = append([]float64(nil), cfg.Prices...)
	m.config = &c
	delete(m.plans, cfg.PlanDay)
	delete(m.tele, cfg.PlanDay)
	delete(m.current, cfg.PlanDay)
	return nil
}

// NextVersionNumber 实现 Store。
func (m *MemoryStore) NextVersionNumber(_ context.Context, planDay string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.plans[planDay]) + 1, nil
}

// InsertVersion 实现 Store。
func (m *MemoryStore) InsertVersion(_ context.Context, v *PlanVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, old := range m.plans[v.PlanDay] {
		old.IsCurrent = false
	}
	vv := *v
	m.plans[v.PlanDay] = append(m.plans[v.PlanDay], &vv)
	m.current[v.PlanDay] = vv.Version
	v.IsCurrent = true
	v.ID = int64(len(m.plans[v.PlanDay]))
	return nil
}

// CurrentVersion 实现 Store。
func (m *MemoryStore) CurrentVersion(_ context.Context, planDay string) (*PlanVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	num, ok := m.current[planDay]
	if !ok {
		return nil, &ErrNotFound{What: "当前计划版本"}
	}
	return m.getLocked(planDay, num)
}

// GetVersion 实现 Store。
func (m *MemoryStore) GetVersion(_ context.Context, planDay string, version int) (*PlanVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getLocked(planDay, version)
}

func (m *MemoryStore) getLocked(planDay string, version int) (*PlanVersion, error) {
	for _, v := range m.plans[planDay] {
		if v.Version == version {
			vv := *v
			return &vv, nil
		}
	}
	return nil, &ErrNotFound{What: "计划版本"}
}

// ListVersions 实现 Store。
func (m *MemoryStore) ListVersions(_ context.Context, planDay string) ([]*PlanVersion, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.plans[planDay]
	out := make([]*PlanVersion, 0, len(src))
	cur := 0
	for i := len(src) - 1; i >= 0; i-- {
		vv := *src[i]
		if vv.IsCurrent {
			cur = vv.Version
		}
		out = append(out, &vv)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, cur, nil
}

// InsertTelemetry 实现 Store。
func (m *MemoryStore) InsertTelemetry(_ context.Context, t Telemetry) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tele[t.PlanDay] == nil {
		m.tele[t.PlanDay] = map[string]Telemetry{}
	}
	if _, dup := m.tele[t.PlanDay][t.ID]; dup {
		return false, nil
	}
	m.tele[t.PlanDay][t.ID] = t
	return true, nil
}

// ListTelemetry 实现 Store。
func (m *MemoryStore) ListTelemetry(_ context.Context, planDay string) ([]Telemetry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.tele[planDay]
	out := make([]Telemetry, 0, len(src))
	for _, t := range src {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Timestamp.Equal(out[j].Timestamp) {
			return out[i].ID < out[j].ID
		}
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out, nil
}
