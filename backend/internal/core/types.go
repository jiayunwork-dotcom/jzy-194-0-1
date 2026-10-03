// Package core 定义跨层共享的领域类型与校验规则。
package core

import "time"

// StationParams 电站参数。
type StationParams struct {
	EnergyMWh             float64 `json:"energy_mwh"`               // 额定能量 MWh
	MaxChargeMW           float64 `json:"max_charge_mw"`            // 最大充电功率 MW
	MaxDischargeMW        float64 `json:"max_discharge_mw"`         // 最大放电功率 MW
	SocMin                float64 `json:"soc_min"`                  // 荷电状态下限 0..1
	SocMax                float64 `json:"soc_max"`                  // 荷电状态上限 0..1
	ChargeEff             float64 `json:"charge_eff"`               // 充电效率 (0,1]
	DischargeEff          float64 `json:"discharge_eff"`            // 放电效率 (0,1]
	DegradationCostPerMWh float64 `json:"degradation_cost_per_mwh"` // 每 MWh 电池侧吞吐量折损成本，元
	EndSocMin             float64 `json:"end_soc_min"`              // 日末荷电状态下限 0..1
	DeviationThresholdMWh float64 `json:"deviation_threshold_mwh"`  // 触发重排的 SoC 偏差阈值 MWh
}

// SlotPlan 单时段计划。
type SlotPlan struct {
	Slot    int     `json:"slot"`     // 0..95
	PowerMW float64 `json:"power_mw"` // 正=放电，负=充电
	SocMWh  float64 `json:"soc_mwh"`  // 时段末计划 SoC
}

// Plan 一个计划版本。
type Plan struct {
	ID              int64      `json:"id"`
	Date            string     `json:"date"` // 计划日 YYYY-MM-DD（计划时区）
	Version         int        `json:"version"`
	TriggerReason   string     `json:"trigger_reason"`
	ExpectedRevenue float64    `json:"expected_revenue"` // 优化视野（重排时为剩余时段）的期望净收益，元
	InitialSocMWh   float64    `json:"initial_soc_mwh"`  // 计划日起始 SoC
	BasedOnTs       *time.Time `json:"based_on_ts"`      // 本版本依据的最新遥测时刻；日前版本为 null
	CreatedAt       time.Time  `json:"created_at"`
	Slots           []SlotPlan `json:"slots,omitempty"`
}

// PlanMeta 版本列表项（不含时段明细）。
type PlanMeta struct {
	ID              int64      `json:"id"`
	Date            string     `json:"date"`
	Version         int        `json:"version"`
	TriggerReason   string     `json:"trigger_reason"`
	ExpectedRevenue float64    `json:"expected_revenue"`
	InitialSocMWh   float64    `json:"initial_soc_mwh"`
	BasedOnTs       *time.Time `json:"based_on_ts"`
	CreatedAt       time.Time  `json:"created_at"`
}

// Telemetry 一条遥测。
type Telemetry struct {
	ID      string    `json:"id"`       // 遥测编号（幂等键）
	Date    string    `json:"date"`     // 所属计划日
	Ts      time.Time `json:"ts"`       // 采集时刻
	Slot    int       `json:"slot"`     // 所属时段 0..95
	SocMWh  float64   `json:"soc_mwh"`  // 实际 SoC
	PowerMW float64   `json:"power_mw"` // 实际功率，正=放电
}
