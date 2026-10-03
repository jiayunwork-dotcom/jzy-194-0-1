// Package service 承载业务逻辑：日前优化、遥测接入（幂等/归位）、
// 偏差判定与滚动重排、计划版本管理。依赖 store.Store 接口，与具体数据库解耦。
package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"essplanner/internal/core"
	"essplanner/internal/optimize"
	"essplanner/internal/store"
)

const (
	SlotsPerDay = core.SlotsPerDay
	SlotHours   = 0.25 // 15 分钟
)

// ErrInfeasible 优化无可行解（HTTP 层映射 422）。
var ErrInfeasible = optimize.ErrInfeasible

type Service struct {
	st         store.Store
	loc        *time.Location // 计划日所属时区
	gridPoints int
}

func New(st store.Store, loc *time.Location, gridPoints int) *Service {
	if gridPoints < 100 {
		gridPoints = 100
	}
	return &Service{st: st, loc: loc, gridPoints: gridPoints}
}

// ---------- 电站参数 ----------

func (s *Service) GetStation(ctx context.Context) (*core.StationParams, error) {
	return s.st.GetStation(ctx)
}

func (s *Service) PutStation(ctx context.Context, p *core.StationParams) error {
	if errs := core.ValidateStation(p); len(errs) > 0 {
		return errs
	}
	return s.st.PutStation(ctx, p)
}

// ---------- 电价 ----------

func (s *Service) GetPrices(ctx context.Context, date string) ([]float64, error) {
	if err := validateDate(date); err != nil {
		return nil, err
	}
	return s.st.GetPrices(ctx, date)
}

func (s *Service) PutPrices(ctx context.Context, date string, prices []float64) error {
	if err := validateDate(date); err != nil {
		return err
	}
	if errs := core.ValidatePrices(prices); len(errs) > 0 {
		return errs
	}
	return s.st.PutPrices(ctx, date, prices)
}

// ---------- 日前优化 ----------

// OptimizeDayAhead 以 initialSocFrac（0..1，相对额定能量）为日起始 SoC，
// 对 date 全天 96 时段求解，并存为新的计划版本。
func (s *Service) OptimizeDayAhead(ctx context.Context, date string, initialSocFrac float64) (*core.Plan, error) {
	if err := validateDate(date); err != nil {
		return nil, err
	}
	st, err := s.st.GetStation(ctx)
	if err != nil {
		return nil, fmt.Errorf("请先录入电站参数: %w", err)
	}
	prices, err := s.st.GetPrices(ctx, date)
	if err != nil {
		return nil, fmt.Errorf("该日期尚无电价数据: %w", err)
	}
	if !(initialSocFrac >= st.SocMin && initialSocFrac <= st.SocMax) {
		return nil, core.ValidationError{{
			Field:   "initial_soc",
			Message: fmt.Sprintf("起始荷电状态必须在 [%.4g, %.4g] 内", st.SocMin, st.SocMax),
		}}
	}
	op := s.optimizeParams(st)
	slots, rev, err := optimize.Optimize(prices, op, initialSocFrac*st.EnergyMWh)
	if err != nil {
		return nil, err
	}
	plan := &core.Plan{
		Date:            date,
		TriggerReason:   "日前优化",
		ExpectedRevenue: rev,
		InitialSocMWh:   initialSocFrac * st.EnergyMWh,
		Slots:           toSlotPlans(slots, 0),
	}
	return s.st.CreatePlan(ctx, plan)
}

// ---------- 遥测与滚动重排 ----------

type TelemetryInput struct {
	ID      string  `json:"id"`
	Ts      string  `json:"ts"` // RFC3339，如 2026-10-03T10:23:00+08:00
	SocMWh  float64 `json:"soc_mwh"`
	PowerMW float64 `json:"power_mw"`
}

type TelemetryResult struct {
	Duplicate    bool    `json:"duplicate"`         // 重复上报，已忽略
	Replanned    bool    `json:"replanned"`         // 是否触发了重排
	Version      int     `json:"version,omitempty"` // 重排产生的新版本号
	DeviationMWh float64 `json:"deviation_mwh"`     // 最新遥测相对计划的 SoC 偏差
	Reason       string  `json:"reason,omitempty"`  // 未触发重排的原因说明
}

// SubmitTelemetry 接收一条遥测：按编号幂等去重，按采集时刻归位；
// 最新遥测与当前计划偏差超阈值时，从其下一时段到日末重新优化并存为新版本。
func (s *Service) SubmitTelemetry(ctx context.Context, date string, in TelemetryInput) (*TelemetryResult, error) {
	if err := validateDate(date); err != nil {
		return nil, err
	}
	st, err := s.st.GetStation(ctx)
	if err != nil {
		return nil, fmt.Errorf("请先录入电站参数: %w", err)
	}
	// 字段校验
	var verrs core.ValidationError
	if in.ID == "" {
		verrs = append(verrs, core.FieldError{Field: "id", Message: "遥测编号不能为空"})
	}
	if len(in.ID) > 128 {
		verrs = append(verrs, core.FieldError{Field: "id", Message: "遥测编号过长"})
	}
	ts, terr := time.Parse(time.RFC3339, in.Ts)
	if terr != nil {
		verrs = append(verrs, core.FieldError{Field: "ts", Message: "时刻须为 RFC3339 格式"})
	}
	if math.IsNaN(in.SocMWh) || math.IsInf(in.SocMWh, 0) || in.SocMWh < 0 || in.SocMWh > st.EnergyMWh {
		verrs = append(verrs, core.FieldError{
			Field:   "soc_mwh",
			Message: fmt.Sprintf("实际 SoC 必须在 [0, %.6g] MWh 内", st.EnergyMWh),
		})
	}
	if math.IsNaN(in.PowerMW) || math.IsInf(in.PowerMW, 0) {
		verrs = append(verrs, core.FieldError{Field: "power_mw", Message: "功率必须为有限数"})
	}
	if len(verrs) > 0 {
		return nil, verrs
	}
	local := ts.In(s.loc)
	if local.Format("2006-01-02") != date {
		return nil, core.ValidationError{{
			Field:   "ts",
			Message: fmt.Sprintf("遥测时刻 %s 不属于计划日 %s", local.Format("2006-01-02"), date),
		}}
	}
	plan, err := s.st.GetCurrentPlan(ctx, date)
	if err != nil {
		return nil, core.ValidationError{{
			Field:   "ts",
			Message: fmt.Sprintf("计划日 %s 尚无计划，无法关联遥测", date),
		}}
	}
	tm := &core.Telemetry{
		ID:      in.ID,
		Date:    date,
		Ts:      ts,
		Slot:    local.Hour()*4 + local.Minute()/15,
		SocMWh:  in.SocMWh,
		PowerMW: in.PowerMW,
	}
	inserted, err := s.st.InsertTelemetry(ctx, tm)
	if err != nil {
		return nil, err
	}
	if !inserted {
		return &TelemetryResult{Duplicate: true, Reason: "重复上报，已忽略"}, nil
	}

	// 偏差判定以“采集时刻最新”的遥测为准；晚到的旧遥测只归位、不触发。
	latest, err := s.st.LatestTelemetry(ctx, date)
	if err != nil {
		return nil, err
	}
	if plan.BasedOnTs != nil && !latest.Ts.After(*plan.BasedOnTs) {
		return &TelemetryResult{Reason: "无更新时刻的遥测，不重新判定"}, nil
	}
	dev := latest.SocMWh - plannedSocAt(plan, latest.Ts.In(s.loc))
	res := &TelemetryResult{DeviationMWh: dev}
	if math.Abs(dev) <= st.DeviationThresholdMWh {
		res.Reason = fmt.Sprintf("偏差 %.3f MWh 未超阈值 %.3f MWh", math.Abs(dev), st.DeviationThresholdMWh)
		return res, nil
	}
	startSlot := latest.Slot + 1 // 当前时段已部分执行，从下一时段起重排
	if startSlot >= SlotsPerDay {
		res.Reason = "已到日末，无需重排"
		return res, nil
	}
	prices, err := s.st.GetPrices(ctx, date)
	if err != nil {
		return nil, err
	}
	op := s.optimizeParams(st)
	slots, rev, err := optimize.Optimize(prices[startSlot:], op, latest.SocMWh)
	if err != nil {
		return nil, fmt.Errorf("滚动重排失败（旧版本保留）: %w", err)
	}
	newSlots := make([]core.SlotPlan, 0, SlotsPerDay)
	newSlots = append(newSlots, plan.Slots[:startSlot]...) // 已过去的时段不许改
	newSlots = append(newSlots, toSlotPlans(slots, startSlot)...)
	basedOn := latest.Ts
	newPlan := &core.Plan{
		Date: date,
		TriggerReason: fmt.Sprintf("遥测 %s：SoC 偏差 %.3f MWh 超阈值 %.3f MWh，自时段 %d 起滚动重排",
			latest.ID, dev, st.DeviationThresholdMWh, startSlot),
		ExpectedRevenue: rev,
		InitialSocMWh:   plan.InitialSocMWh,
		BasedOnTs:       &basedOn,
		Slots:           newSlots,
	}
	created, err := s.st.CreatePlan(ctx, newPlan)
	if err != nil {
		return nil, err
	}
	res.Replanned = true
	res.Version = created.Version
	return res, nil
}

// plannedSocAt 计划 SoC 在时刻 t 的线性插值（计划值为时段末点）。
func plannedSocAt(plan *core.Plan, t time.Time) float64 {
	slot := t.Hour()*4 + t.Minute()/15
	if slot >= len(plan.Slots) {
		slot = len(plan.Slots) - 1
	}
	frac := (float64(t.Minute()%15)*60 + float64(t.Second())) / 900.0
	prev := plan.InitialSocMWh
	if slot > 0 {
		prev = plan.Slots[slot-1].SocMWh
	}
	return prev + frac*(plan.Slots[slot].SocMWh-prev)
}

// ---------- 查询 ----------

func (s *Service) GetCurrentPlan(ctx context.Context, date string) (*core.Plan, error) {
	if err := validateDate(date); err != nil {
		return nil, err
	}
	return s.st.GetCurrentPlan(ctx, date)
}

func (s *Service) GetPlanVersion(ctx context.Context, date string, version int) (*core.Plan, error) {
	if err := validateDate(date); err != nil {
		return nil, err
	}
	return s.st.GetPlanVersion(ctx, date, version)
}

func (s *Service) ListPlanVersions(ctx context.Context, date string) ([]core.PlanMeta, error) {
	if err := validateDate(date); err != nil {
		return nil, err
	}
	return s.st.ListPlanVersions(ctx, date)
}

func (s *Service) ListTelemetry(ctx context.Context, date string) ([]core.Telemetry, error) {
	if err := validateDate(date); err != nil {
		return nil, err
	}
	return s.st.ListTelemetry(ctx, date)
}

// ---------- 辅助 ----------

func (s *Service) optimizeParams(st *core.StationParams) optimize.Params {
	return optimize.Params{
		EnergyMWh:         st.EnergyMWh,
		MaxChargeMW:       st.MaxChargeMW,
		MaxDischargeMW:    st.MaxDischargeMW,
		SocMin:            st.SocMin,
		SocMax:            st.SocMax,
		ChargeEff:         st.ChargeEff,
		DischargeEff:      st.DischargeEff,
		DegradationPerMWh: st.DegradationCostPerMWh,
		EndSocMin:         st.EndSocMin,
		SlotHours:         SlotHours,
		GridPoints:        s.gridPoints,
	}
}

func toSlotPlans(slots []optimize.SlotResult, offset int) []core.SlotPlan {
	out := make([]core.SlotPlan, len(slots))
	for i, sl := range slots {
		out[i] = core.SlotPlan{Slot: offset + i, PowerMW: sl.PowerMW, SocMWh: sl.SocMWh}
	}
	return out
}

func validateDate(date string) error {
	if t, err := time.Parse("2006-01-02", date); err != nil || t.Format("2006-01-02") != date {
		return core.ValidationError{{Field: "date", Message: "日期须为 YYYY-MM-DD 格式"}}
	}
	return nil
}

// IsNotFound 供 HTTP 层判断 404。
func IsNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
