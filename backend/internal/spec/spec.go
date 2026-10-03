// Package spec 定义电站参数、分时电价与遥测数据结构，并负责字段级校验。
package spec

import (
	"fmt"
	"math"
)

const (
	// Periods 一天 96 个 15 分钟时段。
	Periods = 96
	// PeriodHours 每个时段 0.25 小时。
	PeriodHours = 0.25
)

// Station 电站参数。能量单位 MWh，功率单位 MW，效率与 SOC 为无量纲比值。
type Station struct {
	RatedEnergyMWh        float64 `json:"rated_energy_mwh"`
	MaxChargeMW           float64 `json:"max_charge_mw"`
	MaxDischargeMW        float64 `json:"max_discharge_mw"`
	SocMin                float64 `json:"soc_min"`
	SocMax                float64 `json:"soc_max"`
	ChargeEfficiency      float64 `json:"charge_efficiency"`
	DischargeEfficiency   float64 `json:"discharge_efficiency"`
	DegradationCostPerMWh float64 `json:"degradation_cost_per_mwh"`
	SocEndMin             float64 `json:"soc_end_min"`
	SocInitial            float64 `json:"soc_initial"`
	// DeviationThresholdMWh 实际 SOC 与计划偏差超过该值（MWh）触发滚动重优化。
	DeviationThresholdMWh float64 `json:"deviation_threshold_mwh"`
	// GridPoints SOC 网格划分数，0 用默认值。仅影响离散精度，不影响约束。
	GridPoints int `json:"grid_points,omitempty"`
}

// DefaultGridPoints 默认 SOC 网格划分数。
const DefaultGridPoints = 800

// FieldError 携带出错字段名，便于前端逐字段提示。
type FieldError struct {
	Message string   `json:"error"`
	Fields  []string `json:"fields"`
}

func (e *FieldError) Error() string { return e.Message }

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func addField(fields *[]string, name string, cond bool) {
	if cond {
		*fields = append(*fields, name)
	}
}

// Validate 校验全部电站参数，返回所有不合规字段。
func (s Station) Validate() error {
	var bad []string

	addField(&bad, "rated_energy_mwh", !finite(s.RatedEnergyMWh) || s.RatedEnergyMWh <= 0)
	addField(&bad, "max_charge_mw", !finite(s.MaxChargeMW) || s.MaxChargeMW < 0)
	addField(&bad, "max_discharge_mw", !finite(s.MaxDischargeMW) || s.MaxDischargeMW < 0)
	addField(&bad, "charge_efficiency", !finite(s.ChargeEfficiency) || s.ChargeEfficiency <= 0 || s.ChargeEfficiency > 1)
	addField(&bad, "discharge_efficiency", !finite(s.DischargeEfficiency) || s.DischargeEfficiency <= 0 || s.DischargeEfficiency > 1)
	addField(&bad, "degradation_cost_per_mwh", !finite(s.DegradationCostPerMWh) || s.DegradationCostPerMWh < 0)

	socRangeOK := finite(s.SocMin) && finite(s.SocMax) &&
		s.SocMin >= 0 && s.SocMax <= 1 && s.SocMin < s.SocMax
	if !socRangeOK {
		bad = append(bad, "soc_min", "soc_max")
	}

	// 能先确定上下限本身有效，再判断落在区间内的字段。
	if socRangeOK {
		addField(&bad, "soc_end_min", !finite(s.SocEndMin) || s.SocEndMin < s.SocMin || s.SocEndMin > s.SocMax)
		addField(&bad, "soc_initial", !finite(s.SocInitial) || s.SocInitial < s.SocMin || s.SocInitial > s.SocMax)
	} else {
		addField(&bad, "soc_end_min", !finite(s.SocEndMin))
		addField(&bad, "soc_initial", !finite(s.SocInitial))
	}

	addField(&bad, "deviation_threshold_mwh", !finite(s.DeviationThresholdMWh) || s.DeviationThresholdMWh < 0)
	if s.GridPoints != 0 && s.GridPoints < 2 {
		bad = append(bad, "grid_points")
	}

	if len(bad) > 0 {
		return &FieldError{Message: "电站参数校验失败，详见 fields", Fields: bad}
	}
	return nil
}

// EnergyBounds 返回能量域的 SOC 下限、上限、日末下限、初始能量（MWh）。
func (s Station) EnergyBounds() (emin, emax, eend, e0 float64) {
	return s.SocMin * s.RatedEnergyMWh,
		s.SocMax * s.RatedEnergyMWh,
		s.SocEndMin * s.RatedEnergyMWh,
		s.SocInitial * s.RatedEnergyMWh
}

// ValidatePrices 校验分时电价：必须恰好 96 个时段且均为有限值（允许负电价）。
func ValidatePrices(prices []float64) error {
	if len(prices) != Periods {
		return &FieldError{
			Message: fmt.Sprintf("电价必须为 %d 个时段，实际 %d 个", Periods, len(prices)),
			Fields:  []string{"prices"},
		}
	}
	for i, p := range prices {
		if !finite(p) {
			return &FieldError{
				Message: fmt.Sprintf("第 %d 个时段电价非法（NaN/Inf）", i),
				Fields:  []string{fmt.Sprintf("prices[%d]", i), "prices"},
			}
		}
	}
	return nil
}
