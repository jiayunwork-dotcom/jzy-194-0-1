package spec

import (
	"math"
	"strings"
	"testing"
)

func validStation() Station {
	return Station{
		RatedEnergyMWh:        10,
		MaxChargeMW:           5,
		MaxDischargeMW:        5,
		SocMin:                0.1,
		SocMax:                0.9,
		ChargeEfficiency:      0.95,
		DischargeEfficiency:   0.95,
		DegradationCostPerMWh: 10,
		SocEndMin:             0.2,
		SocInitial:            0.5,
		DeviationThresholdMWh: 0.2,
		GridPoints:            100,
	}
}

func TestValidStationPasses(t *testing.T) {
	if err := validStation().Validate(); err != nil {
		t.Fatalf("合法参数被拒收: %v", err)
	}
}

func TestValidationRejectsBadFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Station)
		want   []string
	}{
		{"负能量", func(s *Station) { s.RatedEnergyMWh = -1 }, []string{"rated_energy_mwh"}},
		{"零能量", func(s *Station) { s.RatedEnergyMWh = 0 }, []string{"rated_energy_mwh"}},
		{"负充电功率", func(s *Station) { s.MaxChargeMW = -0.1 }, []string{"max_charge_mw"}},
		{"负放电功率", func(s *Station) { s.MaxDischargeMW = -0.1 }, []string{"max_discharge_mw"}},
		{"充电效率为零", func(s *Station) { s.ChargeEfficiency = 0 }, []string{"charge_efficiency"}},
		{"放电效率大于1", func(s *Station) { s.DischargeEfficiency = 1.1 }, []string{"discharge_efficiency"}},
		{"充电效率为负", func(s *Station) { s.ChargeEfficiency = -0.9 }, []string{"charge_efficiency"}},
		{"SOC下限不低于上限", func(s *Station) { s.SocMin = 0.9; s.SocMax = 0.5 }, []string{"soc_min", "soc_max"}},
		{"SOC下限为负", func(s *Station) { s.SocMin = -0.1 }, []string{"soc_min", "soc_max"}},
		{"SOC上限大于1", func(s *Station) { s.SocMax = 1.2 }, []string{"soc_min", "soc_max"}},
		{"日末下限越界", func(s *Station) { s.SocEndMin = 0.95 }, []string{"soc_end_min"}},
		{"初始SOC越界", func(s *Station) { s.SocInitial = 0.05 }, []string{"soc_initial"}},
		{"负折损", func(s *Station) { s.DegradationCostPerMWh = -1 }, []string{"degradation_cost_per_mwh"}},
		{"网格点太少", func(s *Station) { s.GridPoints = 1 }, []string{"grid_points"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validStation()
			tc.mutate(&s)
			err := s.Validate()
			if err == nil {
				t.Fatalf("应当拒收")
			}
			fe, ok := err.(*FieldError)
			if !ok {
				t.Fatalf("错误类型不是 *FieldError: %T", err)
			}
			for _, w := range tc.want {
				if !contains(fe.Fields, w) {
					t.Fatalf("错误字段 %v 中缺少 %s", fe.Fields, w)
				}
			}
		})
	}
}

func TestValidatePrices(t *testing.T) {
	good := make([]float64, Periods)
	if err := ValidatePrices(good); err != nil {
		t.Fatalf("96 个价格应通过: %v", err)
	}

	for n := range map[int]bool{0: true, 95: true, 97: true, 192: true} {
		if err := ValidatePrices(make([]float64, n)); err == nil {
			t.Fatalf("%d 个价格应被拒收", n)
		} else if fe, ok := err.(*FieldError); !ok || !contains(fe.Fields, "prices") {
			t.Fatalf("应指出 prices 字段")
		}
	}

	bad := make([]float64, Periods)
	bad[50] = math.NaN()
	err := ValidatePrices(bad)
	if err == nil || !strings.Contains(err.Error(), "50") {
		t.Fatalf("应指出第 50 个时段非法，实际: %v", err)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
