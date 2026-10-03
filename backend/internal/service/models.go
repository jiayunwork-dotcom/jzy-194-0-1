// Package service 承载储能电站日前计划与滚动修正的业务逻辑。
package service

import (
	"time"

	"energystorage/internal/spec"
)

// Config 某一计划日使用的电站参数与电价。
type Config struct {
	Station   spec.Station `json:"station"`
	Prices    []float64    `json:"prices"`
	PlanDay   string       `json:"plan_day"` // YYYY-MM-DD，按 Location 解释为计划日
	CreatedAt time.Time    `json:"created_at,omitempty"`
}

// PlanVersion 一个计划版本。完整 96 点视图由 Frozen*（过去时段）+ 优化尾部拼接而成。
type PlanVersion struct {
	ID                int64     `json:"id"`
	PlanDay           string    `json:"plan_day"`
	Version           int       `json:"version"`
	IsCurrent         bool      `json:"is_current"`
	TriggerReason     string    `json:"trigger_reason"`
	StartPeriod       int       `json:"start_period"`
	StartEnergyMWh    float64   `json:"start_energy_mwh"`
	ChargeMW          []float64 `json:"charge_mw"`
	DischargeMW       []float64 `json:"discharge_mw"`
	Soc               []float64 `json:"soc"`
	FrozenChargeMW    []float64 `json:"frozen_charge_mw"`
	FrozenDischargeMW []float64 `json:"frozen_discharge_mw"`
	FrozenSoc         []float64 `json:"frozen_soc"`
	ProfitYuan        float64   `json:"profit_yuan"`
	GridPoints        int       `json:"grid_points"`
	GridGapMWh        float64   `json:"grid_gap_mwh"`
	ErrorBoundYuan    float64   `json:"error_bound_yuan"`
	CreatedAt         time.Time `json:"created_at"`
}

// FullCharge 拼接冻结前缀与优化尾部，返回完整 96 点计划；过去时段不允许再改。
func (v *PlanVersion) FullCharge() []float64 { return merge96(v.FrozenChargeMW, v.ChargeMW) }

// FullDischarge 同 FullCharge。
func (v *PlanVersion) FullDischarge() []float64 { return merge96(v.FrozenDischargeMW, v.DischargeMW) }

// FullSoc 同 FullCharge。FrozenSoc 为冻结段各时段末 SOC，尾部含 StartPeriod 时段末 SOC。
func (v *PlanVersion) FullSoc() []float64 { return merge96(v.FrozenSoc, v.Soc) }

func merge96(frozen, tail []float64) []float64 {
	out := make([]float64, spec.Periods)
	copy(out, frozen)
	copy(out[len(frozen):], tail)
	return out
}

// Telemetry 一条遥测记录。
type Telemetry struct {
	ID          string    `json:"id"`
	PlanDay     string    `json:"plan_day"`
	Timestamp   time.Time `json:"timestamp"`
	PeriodIndex int       `json:"period_index"`
	Soc         float64   `json:"soc"`
	ChargeMW    float64   `json:"charge_mw"`
	DischargeMW float64   `json:"discharge_mw"`
	ReceivedAt  time.Time `json:"received_at,omitempty"`
	Duplicate   bool      `json:"-"` // 命中同编号旧记录
}

// ReoptWarning 遥测已收录但无法从当前状态优化出可行计划时给出的提示。
type ReoptWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// IngestResult 遥测处理结果。
type IngestResult struct {
	Telemetry    *Telemetry    `json:"telemetry"`
	Duplicate    bool          `json:"duplicate"`
	Reoptimized  bool          `json:"reoptimized"`
	NewVersion   *PlanVersion  `json:"new_version,omitempty"`
	DeviationMWh float64       `json:"deviation_mwh,omitempty"`
	Warning      *ReoptWarning `json:"warning,omitempty"`
}
