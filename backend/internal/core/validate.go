package core

import (
	"fmt"
	"math"
	"strings"
)

// FieldError 单字段校验错误。
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError 校验错误集合，HTTP 层映射为 400。
type ValidationError []FieldError

func (v ValidationError) Error() string {
	parts := make([]string, len(v))
	for i, e := range v {
		parts[i] = e.Field + ": " + e.Message
	}
	return "参数校验失败: " + strings.Join(parts, "; ")
}

// SlotsPerDay 每日时段数（15 分钟 × 96）。
const SlotsPerDay = 96

// ValidateStation 校验电站参数，返回全部字段错误。
func ValidateStation(p *StationParams) ValidationError {
	var errs ValidationError
	bad := func(field, msg string) {
		errs = append(errs, FieldError{Field: field, Message: msg})
	}
	if !(p.EnergyMWh > 0) {
		bad("energy_mwh", "额定能量必须为正数")
	}
	if p.MaxChargeMW < 0 || math.IsNaN(p.MaxChargeMW) {
		bad("max_charge_mw", "最大充电功率不能为负")
	}
	if p.MaxDischargeMW < 0 || math.IsNaN(p.MaxDischargeMW) {
		bad("max_discharge_mw", "最大放电功率不能为负")
	}
	if !(p.SocMin >= 0 && p.SocMin <= 1) {
		bad("soc_min", "荷电状态下限必须在 [0,1] 内")
	}
	if !(p.SocMax >= 0 && p.SocMax <= 1) {
		bad("soc_max", "荷电状态上限必须在 [0,1] 内")
	}
	if p.SocMin >= p.SocMax {
		bad("soc_min", "荷电状态下限必须小于上限")
	}
	if !(p.ChargeEff > 0 && p.ChargeEff <= 1) {
		bad("charge_eff", "充电效率必须在 (0,1] 内")
	}
	if !(p.DischargeEff > 0 && p.DischargeEff <= 1) {
		bad("discharge_eff", "放电效率必须在 (0,1] 内")
	}
	if p.DegradationCostPerMWh < 0 || math.IsNaN(p.DegradationCostPerMWh) {
		bad("degradation_cost_per_mwh", "折损成本不能为负")
	}
	if !(p.EndSocMin >= 0 && p.EndSocMin <= 1) {
		bad("end_soc_min", "日末荷电状态下限必须在 [0,1] 内")
	} else if p.SocMax <= 1 && p.EndSocMin > p.SocMax {
		bad("end_soc_min", "日末荷电状态下限不能高于荷电状态上限，否则无解")
	}
	if !(p.DeviationThresholdMWh > 0) {
		bad("deviation_threshold_mwh", "偏差阈值必须为正数")
	}
	return errs
}

// ValidatePrices 校验电价：必须恰好 96 个时段且均为有限数。
func ValidatePrices(prices []float64) ValidationError {
	var errs ValidationError
	if len(prices) != SlotsPerDay {
		errs = append(errs, FieldError{
			Field:   "prices",
			Message: fmt.Sprintf("电价必须恰好 %d 个时段，实际 %d 个", SlotsPerDay, len(prices)),
		})
		return errs
	}
	for i, v := range prices {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("prices[%d]", i),
				Message: "电价必须为有限数",
			})
		}
	}
	return errs
}
