package core_test

import (
	"testing"

	"essplanner/internal/core"
)

func validStation() *core.StationParams {
	return &core.StationParams{
		EnergyMWh: 10, MaxChargeMW: 5, MaxDischargeMW: 5,
		SocMin: 0.1, SocMax: 0.9,
		ChargeEff: 0.9, DischargeEff: 0.9,
		DegradationCostPerMWh: 2, EndSocMin: 0.2,
		DeviationThresholdMWh: 0.5,
	}
}

func TestValidateStation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*core.StationParams)
		fields []string
	}{
		{"充电效率为0", func(p *core.StationParams) { p.ChargeEff = 0 }, []string{"charge_eff"}},
		{"充电效率大于1", func(p *core.StationParams) { p.ChargeEff = 1.01 }, []string{"charge_eff"}},
		{"放电效率为负", func(p *core.StationParams) { p.DischargeEff = -0.5 }, []string{"discharge_eff"}},
		{"效率边界1合法", func(p *core.StationParams) { p.ChargeEff, p.DischargeEff = 1, 1 }, nil},
		{"下限不低于上限", func(p *core.StationParams) { p.SocMin, p.SocMax = 0.9, 0.9 }, []string{"soc_min"}},
		{"下限大于上限", func(p *core.StationParams) { p.SocMin, p.SocMax = 0.95, 0.9 }, []string{"soc_min"}},
		{"能量为负", func(p *core.StationParams) { p.EnergyMWh = -1 }, []string{"energy_mwh"}},
		{"能量为零", func(p *core.StationParams) { p.EnergyMWh = 0 }, []string{"energy_mwh"}},
		{"充电功率为负", func(p *core.StationParams) { p.MaxChargeMW = -1 }, []string{"max_charge_mw"}},
		{"放电功率为负", func(p *core.StationParams) { p.MaxDischargeMW = -0.1 }, []string{"max_discharge_mw"}},
		{"折损为负", func(p *core.StationParams) { p.DegradationCostPerMWh = -1 }, []string{"degradation_cost_per_mwh"}},
		{"日末下限超上限", func(p *core.StationParams) { p.EndSocMin = 0.95 }, []string{"end_soc_min"}},
		{"偏差阈值非正", func(p *core.StationParams) { p.DeviationThresholdMWh = 0 }, []string{"deviation_threshold_mwh"}},
	}
	for _, c := range cases {
		p := validStation()
		c.mutate(p)
		errs := core.ValidateStation(p)
		if len(c.fields) == 0 {
			if len(errs) != 0 {
				t.Fatalf("%s: 不应有错误, 得到 %v", c.name, errs)
			}
			continue
		}
		for _, f := range c.fields {
			found := false
			for _, e := range errs {
				if e.Field == f {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s: 应指出字段 %s, 得到 %v", c.name, f, errs)
			}
		}
	}
}

func TestValidatePrices(t *testing.T) {
	if errs := core.ValidatePrices(make([]float64, 95)); len(errs) == 0 || errs[0].Field != "prices" {
		t.Fatalf("95 个时段应拒收并指出 prices, 得到 %v", errs)
	}
	if errs := core.ValidatePrices(make([]float64, 97)); len(errs) == 0 {
		t.Fatal("97 个时段应拒收")
	}
	good := make([]float64, 96)
	if errs := core.ValidatePrices(good); len(errs) != 0 {
		t.Fatalf("96 个时段应通过, 得到 %v", errs)
	}
}
