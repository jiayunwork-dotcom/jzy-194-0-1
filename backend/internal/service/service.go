package service

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"energystorage/internal/optimize"
	"energystorage/internal/spec"
)

// Service 业务编排层。单实例部署，用按计划日的互斥锁串行化遥测触发的重优化。
type Service struct {
	store   Store
	loc     *time.Location
	now     Clock
	muByDay sync.Map // planDay -> *sync.Mutex
}

// New 构造 Service。loc 为 nil 时用 Asia/Shanghai。
func New(st Store, loc *time.Location, now Clock) *Service {
	if loc == nil {
		loc = time.FixedZone("CST", 8*60*60)
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: st, loc: loc, now: now}
}

func (s *Service) dayLock(day string) *sync.Mutex {
	m, _ := s.muByDay.LoadOrStore(day, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// Configure 录入/替换电站参数与电价，并生成该计划日的版本 1（日前计划）。
func (s *Service) Configure(ctx context.Context, stn spec.Station, prices []float64, planDay string) (*Config, *PlanVersion, error) {
	if err := stn.Validate(); err != nil {
		return nil, nil, err
	}
	if err := spec.ValidatePrices(prices); err != nil {
		return nil, nil, err
	}
	if _, err := time.ParseInLocation("2006-01-02", planDay, s.loc); err != nil {
		return nil, nil, &spec.FieldError{Message: "plan_day 格式应为 YYYY-MM-DD", Fields: []string{"plan_day"}}
	}

	_, _, _, e0 := stn.EnergyBounds()
	// 先验证可行性，避免清空已有配置后才发现无解。
	probe, err := optimize.Solve(stn, prices, Options0(e0, stn.GridPoints))
	if err != nil {
		return nil, nil, err
	}

	cfg := Config{Station: stn, Prices: append([]float64(nil), prices...), PlanDay: planDay}
	if err := s.store.SaveConfig(ctx, cfg); err != nil {
		return nil, nil, err
	}

	mu := s.dayLock(planDay)
	mu.Lock()
	defer mu.Unlock()

	v := versionFromPlan(probe, planDay, 1, true, "日前计划", e0)
	if err := s.store.InsertVersion(ctx, v); err != nil {
		return nil, nil, err
	}
	return &cfg, v, nil
}

// GetConfig 返回当前配置。
func (s *Service) GetConfig(ctx context.Context) (*Config, error) {
	cfg, err := s.store.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// CurrentPlan 当前计划版本（可能为 nil + ErrNotFound）。
func (s *Service) CurrentPlan(ctx context.Context, planDay string) (*PlanVersion, error) {
	return s.store.CurrentVersion(ctx, planDay)
}

// PlanByVersion 取指定版本；version<=0 等价于当前版本。
func (s *Service) PlanByVersion(ctx context.Context, planDay string, version int) (*PlanVersion, error) {
	if version <= 0 {
		return s.store.CurrentVersion(ctx, planDay)
	}
	return s.store.GetVersion(ctx, planDay, version)
}

// ListVersions 计划版本列表及当前版本号。
func (s *Service) ListVersions(ctx context.Context, planDay string) ([]*PlanVersion, int, error) {
	return s.store.ListVersions(ctx, planDay)
}

// ListTelemetry 遥测列表（时刻升序，晚到数据已按时刻归位）。
func (s *Service) ListTelemetry(ctx context.Context, planDay string) ([]Telemetry, error) {
	return s.store.ListTelemetry(ctx, planDay)
}

// ManualReoptimize 运行员手工触发：按当前时间/最新遥测冻结过去时段后重算。
func (s *Service) ManualReoptimize(ctx context.Context, planDay, reason string) (*PlanVersion, error) {
	if reason == "" {
		reason = "人工触发滚动重优化"
	}
	tels, err := s.store.ListTelemetry(ctx, planDay)
	if err != nil {
		return nil, err
	}
	cfg, err := s.store.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	cut := -1
	for i := range tels {
		if tels[i].PeriodIndex > cut {
			cut = tels[i].PeriodIndex
		}
	}
	if nowIdx := s.periodAt(s.now()); nowIdx >= 0 && nowIdx > cut {
		cut = nowIdx
	}
	if cut < 0 {
		return nil, &ErrConflict{Msg: "尚无已发生时段可冻结，无法滚动重优化"}
	}
	mu := s.dayLock(planDay)
	mu.Lock()
	defer mu.Unlock()
	cur, err := s.store.CurrentVersion(ctx, planDay)
	if err != nil {
		return nil, err
	}
	return s.reoptimizeLocked(ctx, cfg, cur, tels, cut, reason)
}

// IngestTelemetry 收录一条遥测：幂等去重、按时刻归位、偏差超阈值时滚动重优化。
func (s *Service) IngestTelemetry(ctx context.Context, t Telemetry) (*IngestResult, error) {
	cfg, err := s.store.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.validateTelemetry(cfg, &t); err != nil {
		return nil, err
	}
	t.PlanDay = cfg.PlanDay

	mu := s.dayLock(cfg.PlanDay)
	mu.Lock()
	defer mu.Unlock()

	inserted, err := s.store.InsertTelemetry(ctx, t)
	if err != nil {
		return nil, err
	}
	res := &IngestResult{Telemetry: &t}
	if !inserted {
		res.Duplicate = true // 重复遥测编号：只算一次，不再评估偏差
		return res, nil
	}

	cur, err := s.store.CurrentVersion(ctx, cfg.PlanDay)
	if err != nil {
		return nil, err
	}
	if t.PeriodIndex < cur.StartPeriod || t.PeriodIndex >= spec.Periods-1 {
		// 晚到且落在已冻结段，或已是最后一个时段：仅归位存档，不触发重算。
		return res, nil
	}

	plannedEnergy := cur.FullSoc()[t.PeriodIndex] * cfg.Station.RatedEnergyMWh
	actualEnergy := t.Soc * cfg.Station.RatedEnergyMWh
	dev := math.Abs(actualEnergy - plannedEnergy)
	res.DeviationMWh = dev
	thr := cfg.Station.DeviationThresholdMWh
	if dev <= thr+1e-9 {
		return res, nil
	}

	tels, err := s.store.ListTelemetry(ctx, cfg.PlanDay)
	if err != nil {
		return nil, err
	}
	reason := fmt.Sprintf("遥测 %s（时段 %d）实际 SOC 偏差 %.3f MWh，超过阈值 %.3f MWh",
		t.ID, t.PeriodIndex, dev, thr)
	nv, err := s.reoptimizeLocked(ctx, cfg, cur, tels, t.PeriodIndex, reason)
	if err != nil {
		res.Warning = &ReoptWarning{
			Code:    "reoptimize_infeasible",
			Message: "偏差超阈值，但从当前状态无可行计划：" + err.Error(),
		}
		return res, nil
	}
	res.Reoptimized = true
	res.NewVersion = nv
	return res, nil
}

// reoptimizeLocked 要求调用方持有当日锁。cutPeriod 为要冻结的最后一个时段。
func (s *Service) reoptimizeLocked(ctx context.Context, cfg *Config, cur *PlanVersion, tels []Telemetry, cutPeriod int, reason string) (*PlanVersion, error) {
	newStart := cutPeriod + 1
	if newStart < cur.StartPeriod {
		newStart = cur.StartPeriod
	}
	if newStart >= spec.Periods {
		return nil, &ErrConflict{Msg: "当日时段已结束，无法滚动重优化"}
	}

	fCh, fDch, fSoc, startEnergy := buildFrozenPrefix(cfg.Station, tels, newStart)
	plan, err := optimize.Solve(cfg.Station, cfg.Prices, Options(newStart, startEnergy, cfg.Station.GridPoints))
	if err != nil {
		return nil, err
	}
	num, err := s.store.NextVersionNumber(ctx, cfg.PlanDay)
	if err != nil {
		return nil, err
	}
	v := versionFromPlan(plan, cfg.PlanDay, num, true, reason, startEnergy)
	v.FrozenChargeMW = fCh
	v.FrozenDischargeMW = fDch
	v.FrozenSoc = fSoc
	if err := s.store.InsertVersion(ctx, v); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Service) validateTelemetry(cfg *Config, t *Telemetry) error {
	var bad []string
	if t.ID == "" {
		bad = append(bad, "id")
	}
	idx, ok := s.periodOf(cfg.PlanDay, t.Timestamp)
	if !ok {
		bad = append(bad, "timestamp")
	} else {
		t.PeriodIndex = idx
	}
	if math.IsNaN(t.Soc) || math.IsInf(t.Soc, 0) || t.Soc < 0 || t.Soc > 1 {
		bad = append(bad, "soc")
	}
	if math.IsNaN(t.ChargeMW) || math.IsInf(t.ChargeMW, 0) || t.ChargeMW < 0 {
		bad = append(bad, "charge_mw")
	}
	if math.IsNaN(t.DischargeMW) || math.IsInf(t.DischargeMW, 0) || t.DischargeMW < 0 {
		bad = append(bad, "discharge_mw")
	}
	if len(bad) > 0 {
		return &spec.FieldError{Message: "遥测字段校验失败，详见 fields", Fields: bad}
	}
	return nil
}

func (s *Service) periodOf(planDay string, ts time.Time) (int, bool) {
	day, err := time.ParseInLocation("2006-01-02", planDay, s.loc)
	if err != nil {
		return -1, false
	}
	local := ts.In(s.loc)
	if local.Before(day) || !local.Before(day.Add(24*time.Hour)) {
		return -1, false
	}
	idx := int(local.Sub(day) / (15 * time.Minute))
	if idx < 0 || idx >= spec.Periods {
		return -1, false
	}
	return idx, true
}

// periodAt 返回某时刻在当前/最近计划日的时段索引；不属于任何日则 -1。
func (s *Service) periodAt(t time.Time) int {
	local := t.In(s.loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.loc)
	idx := int(local.Sub(day) / (15 * time.Minute))
	if idx < 0 || idx >= spec.Periods {
		return -1
	}
	return idx
}

// buildFrozenPrefix 用遥测重建 [0,newStart) 的实际轨迹；缺测点按上一已知值保持。
// 返回冻结段充电/放电功率、时段末 SOC，以及尾部优化的起始能量（MWh）。
func buildFrozenPrefix(stn spec.Station, tels []Telemetry, newStart int) (ch, dch, soc []float64, startEnergy float64) {
	ch = make([]float64, newStart)
	dch = make([]float64, newStart)
	soc = make([]float64, newStart)

	latest := make([]int, newStart) // 每个时段最后一条遥测在 tels 中的下标
	for i := range latest {
		latest[i] = -1
	}
	for i, t := range tels {
		if t.PeriodIndex >= 0 && t.PeriodIndex < newStart && (latest[t.PeriodIndex] == -1 || t.Timestamp.After(tels[latest[t.PeriodIndex]].Timestamp)) {
			latest[t.PeriodIndex] = i
		}
	}

	lastCh, lastDch, lastSoc := 0.0, 0.0, stn.SocInitial
	startEnergy = stn.SocInitial * stn.RatedEnergyMWh
	for k := 0; k < newStart; k++ {
		if idx := latest[k]; idx >= 0 {
			lastCh = tels[idx].ChargeMW
			lastDch = tels[idx].DischargeMW
			lastSoc = tels[idx].Soc
		}
		ch[k] = lastCh
		dch[k] = lastDch
		soc[k] = lastSoc
	}
	startEnergy = lastSoc * stn.RatedEnergyMWh
	return
}

// Options0 / Options 构造 optimize.Options，避免上层直接依赖 optimize 包细节。
func Options0(e0 float64, grid int) optimize.Options { return Options(0, e0, grid) }

// Options 构造滚动优化选项。
func Options(start int, e0 float64, grid int) optimize.Options {
	return optimize.Options{StartPeriod: start, StartEnergy: e0, GridPoints: grid}
}

func versionFromPlan(p *optimize.Plan, day string, num int, current bool, reason string, e0 float64) *PlanVersion {
	return &PlanVersion{
		PlanDay:        day,
		Version:        num,
		IsCurrent:      current,
		TriggerReason:  reason,
		StartPeriod:    p.Start,
		StartEnergyMWh: e0,
		ChargeMW:       append([]float64(nil), p.ChargeMW...),
		DischargeMW:    append([]float64(nil), p.DischargeMW...),
		Soc:            append([]float64(nil), p.Soc...),
		ProfitYuan:     p.ProfitYuan,
		GridPoints:     p.GridPoints,
		GridGapMWh:     p.GridGapMWh,
		ErrorBoundYuan: p.ErrorBound,
	}
}
