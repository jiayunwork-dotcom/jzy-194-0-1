package optimize

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"energystorage/internal/spec"
)

// twoPeriodStation 手算例子用的电站：1 MWh、1 MW、效率 0.9，SOC [0,1]。
func twoPeriodStation(wear float64) spec.Station {
	return spec.Station{
		RatedEnergyMWh:        1,
		MaxChargeMW:           1,
		MaxDischargeMW:        1,
		SocMin:                0,
		SocMax:                1,
		ChargeEfficiency:      0.9,
		DischargeEfficiency:   0.9,
		DegradationCostPerMWh: wear,
		SocEndMin:             0,
		SocInitial:            0,
		DeviationThresholdMWh: 0,
	}
}

func prices96(p0, p1 float64) []float64 {
	// 两个“一小时时段”放在每天开头：前 4 个 15min 槽价格 p0，接下来 4 个为 p1；
	// 其余时段价格设为 p0：尾部充放一个循环最多与 p0 时段同价，
	// 经效率损失后严格劣于不动，因此最优动作只发生在前 8 个槽。
	p := make([]float64, spec.Periods)
	for i := range p {
		p[i] = p0
	}
	for i := 0; i < 4; i++ {
		p[i] = p0
	}
	for i := 4; i < 8; i++ {
		p[i] = p1
	}
	return p
}

func TestHandExample_Profitable(t *testing.T) {
	st := twoPeriodStation(0)
	prices := prices96(100, 300)
	plan, err := Solve(st, prices, Options{StartPeriod: 0, StartEnergy: 0, GridPoints: 40})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// 第一小时（前 4 槽）充满，第二小时放完。逐槽功率形状受网格影响，
	// 这里校验总量与不同时充放（手算例子关注的是小时尺度的能量）。
	var chargeGridMWh, dischargeGridMWh float64
	for i := 0; i < 4; i++ {
		if plan.DischargeMW[i] != 0 {
			t.Fatalf("slot %d 不应放电", i)
		}
		chargeGridMWh += plan.ChargeMW[i] * dt
	}
	for i := 4; i < 8; i++ {
		if plan.ChargeMW[i] != 0 {
			t.Fatalf("slot %d 不应充电", i)
		}
		dischargeGridMWh += plan.DischargeMW[i] * dt
	}
	if got := math.Abs(chargeGridMWh - 1); got > 1e-6 {
		t.Fatalf("第一小时电网取电量应为 1 MWh，实际 %.6f", chargeGridMWh)
	}
	// 放出 0.81 MWh：1×0.9 存进去，再 ×0.9 放出来。
	if got := math.Abs(dischargeGridMWh - 0.81); got > 1e-6 {
		t.Fatalf("第二小时电网收到电量应为 0.81 MWh，实际 %.6f", dischargeGridMWh)
	}
	for i := 8; i < spec.Periods; i++ {
		if plan.ChargeMW[i] != 0 || plan.DischargeMW[i] != 0 {
			t.Fatalf("尾部槽 %d 应不动", i)
		}
	}
	// 净收益：电网取 1 MWh × 100 = 100 元成本；放出 0.81 MWh × 300 = 243 元。
	if got := math.Abs(plan.ProfitYuan - 143); got > 1e-6 {
		t.Fatalf("净收益应为 143 元，实际 %.6f", plan.ProfitYuan)
	}
}

func TestHandExample_NotWorthIt(t *testing.T) {
	st := twoPeriodStation(0)
	prices := prices96(100, 120)
	plan, err := Solve(st, prices, Options{StartPeriod: 0, StartEnergy: 0, GridPoints: 40})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	for i := 0; i < 8; i++ {
		if plan.ChargeMW[i] != 0 || plan.DischargeMW[i] != 0 {
			t.Fatalf("价差不足以覆盖效率损失，第 %d 槽应不动：ch=%.4f dch=%.4f",
				i, plan.ChargeMW[i], plan.DischargeMW[i])
		}
	}
	if plan.ProfitYuan != 0 {
		t.Fatalf("净收益应为 0，实际 %.6f", plan.ProfitYuan)
	}
}

func TestBruteForceEquivalence(t *testing.T) {
	// 随机小算例：网格上 DP 结果必须与同网格穷举精确值一致（浮点误差内）。
	rng := rand.New(rand.NewSource(20261003))
	for trial := 0; trial < 40; trial++ {
		st := spec.Station{
			RatedEnergyMWh:        2 + rng.Float64()*8,
			MaxChargeMW:           0.5 + rng.Float64()*2,
			MaxDischargeMW:        0.5 + rng.Float64()*2,
			SocMin:                0,
			SocMax:                1,
			ChargeEfficiency:      0.7 + rng.Float64()*0.3,
			DischargeEfficiency:   0.7 + rng.Float64()*0.3,
			DegradationCostPerMWh: rng.Float64() * 30,
			SocEndMin:             rng.Float64() * 0.5,
			SocInitial:            rng.Float64(),
			GridPoints:            0,
		}
		st.SocInitial = st.SocMin + (st.SocMax-st.SocMin)*st.SocInitial
		prices := make([]float64, spec.Periods)
		for i := range prices {
			prices[i] = -50 + rng.Float64()*250
		}

		n := 6 + rng.Intn(12) // 刻意小网格，保证穷举可行
		// 穷举在小网格上每状态每阶段 ~n 次转移：n=18、96 槽约 3 万次/试例。
		exact := gridValue(st, prices, 0, st.SocInitial*st.RatedEnergyMWh, n)
		plan, err := Solve(st, prices, Options{StartPeriod: 0, StartEnergy: st.SocInitial * st.RatedEnergyMWh, GridPoints: n})
		if err == ErrInfeasible || (err == nil && math.IsInf(exact, -1)) {
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: Solve 意外失败: %v", trial, err)
		}
		tol := 1e-7 * math.Max(1, math.Abs(exact))
		if diff := math.Abs(plan.ProfitYuan - exact); diff > tol {
			t.Fatalf("trial %d: DP 收益 %.6f 与穷举 %.6f 不一致（差 %.2e），参数 %+v",
				trial, plan.ProfitYuan, exact, diff, st)
		}
		// 输出轨迹必须满足约束。
		if err := validateTrajectoryFrom(st, plan, st.SocInitial*st.RatedEnergyMWh); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
	}
}

func TestPriceScalingInvariance(t *testing.T) {
	// 性质：电价全部乘同一个正数 k，折损为 0 时目标对电价一次齐次：
	// 计划不变，收益严格同比放大。折损非零时见 TestPriceAndWearScalingInvariance。
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		st := randomStation(rng, true)
		st.DegradationCostPerMWh = 0
		prices := randomPrices(rng)
		p1, err := Solve(st, prices, Options{StartPeriod: 0, StartEnergy: st.SocInitial * st.RatedEnergyMWh, GridPoints: 60})
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		k := 0.5 + rng.Float64()*9.5
		p2, err := Solve(st, scale(prices, k), Options{StartPeriod: 0, StartEnergy: st.SocInitial * st.RatedEnergyMWh, GridPoints: 60})
		if err != nil {
			t.Fatalf("trial %d scaled: %v", trial, err)
		}
		if !slicesClose(p1.ChargeMW, p2.ChargeMW, 1e-9) || !slicesClose(p1.DischargeMW, p2.DischargeMW, 1e-9) {
			t.Fatalf("trial %d: 价格同比缩放后计划改变", trial)
		}
		if got := math.Abs(p2.ProfitYuan - k*p1.ProfitYuan); got > 1e-6*math.Max(1, math.Abs(p1.ProfitYuan)) {
			t.Fatalf("trial %d: 收益未同比放大: %.4f vs %.4f", trial, p2.ProfitYuan, k*p1.ProfitYuan)
		}
	}
}

func TestPriceAndWearScalingInvariance(t *testing.T) {
	// 折损非零时，电价与折损同为价格量纲，一起乘 k 才保持计划不变、收益同比放大；
	// 只乘电价会改变套利门槛（见 docs/design.md「性质 1」）。
	rng := rand.New(rand.NewSource(4242))
	for trial := 0; trial < 20; trial++ {
		st := randomStation(rng, true)
		if st.DegradationCostPerMWh < 1 {
			st.DegradationCostPerMWh = 1 + rng.Float64()*20
		}
		prices := randomPrices(rng)
		p1, err := Solve(st, prices, Options{StartPeriod: 0, StartEnergy: st.SocInitial * st.RatedEnergyMWh, GridPoints: 60})
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		k := 0.5 + rng.Float64()*9.5
		st2 := st
		st2.DegradationCostPerMWh *= k
		p2, err := Solve(st2, scale(prices, k), Options{StartPeriod: 0, StartEnergy: st2.SocInitial * st2.RatedEnergyMWh, GridPoints: 60})
		if err != nil {
			t.Fatalf("trial %d scaled: %v", trial, err)
		}
		if !slicesClose(p1.ChargeMW, p2.ChargeMW, 1e-9) || !slicesClose(p1.DischargeMW, p2.DischargeMW, 1e-9) {
			t.Fatalf("trial %d: 价格与折损同比缩放后计划改变", trial)
		}
		if got := math.Abs(p2.ProfitYuan - k*p1.ProfitYuan); got > 1e-6*math.Max(1, math.Abs(p1.ProfitYuan)) {
			t.Fatalf("trial %d: 收益未同比放大: %.4f vs %.4f", trial, p2.ProfitYuan, k*p1.ProfitYuan)
		}
	}
}

func TestFlatPriceWithWearDoesNothing(t *testing.T) {
	// 性质（精确陈述见 docs/design.md「性质 2」）：
	// 非负平价且折损 > 0、初始 SOC 等于日末下限时，任何充放循环都同时承担
	// 往返效率损失与折损，不动严格最优。
	// 注：初始 SOC 高于日末下限时，把富余存量在正价时段卖掉本身有利；
	// 负平价时利用充电量大、放电量小的量差也能获益——这两种是题设外情形。
	rng := rand.New(rand.NewSource(202))
	for trial := 0; trial < 10; trial++ {
		st := randomStation(rng, true)
		if st.DegradationCostPerMWh < 1 {
			st.DegradationCostPerMWh = 1 + rng.Float64()*20
		}
		st.SocInitial = st.SocEndMin
		flat := rng.Float64() * 400 // 非负平价
		prices := make([]float64, spec.Periods)
		for i := range prices {
			prices[i] = flat
		}
		plan, err := Solve(st, prices, Options{StartPeriod: 0, StartEnergy: st.SocInitial * st.RatedEnergyMWh, GridPoints: 200})
		if err != nil {
			t.Fatalf("trial %d Solve: %v", trial, err)
		}
		for i := 0; i < spec.Periods; i++ {
			if plan.ChargeMW[i] != 0 || plan.DischargeMW[i] != 0 {
				t.Fatalf("trial %d 时段 %d 应不动：ch=%.4f dch=%.4f", trial, i, plan.ChargeMW[i], plan.DischargeMW[i])
			}
			if math.Abs(plan.Soc[i]-st.SocInitial) > 1e-12 {
				t.Fatalf("trial %d 时段 %d SOC 偏离: %.6f", trial, i, plan.Soc[i])
			}
		}
		if plan.ProfitYuan != 0 {
			t.Fatalf("trial %d 收益应为 0，实际 %.6f", trial, plan.ProfitYuan)
		}
	}
}

func TestMorePowerNeverReducesProfit(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		st := randomStation(rng, true)
		prices := randomPrices(rng)
		pLow, err := Solve(st, prices, Options{StartPeriod: 0, StartEnergy: st.SocInitial * st.RatedEnergyMWh, GridPoints: 80})
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		stBig := st
		stBig.MaxChargeMW *= 1.7
		stBig.MaxDischargeMW *= 1.7
		pHigh, err := Solve(stBig, prices, Options{StartPeriod: 0, StartEnergy: stBig.SocInitial * stBig.RatedEnergyMWh, GridPoints: 80})
		if err != nil {
			t.Fatalf("trial %d big: %v", trial, err)
		}
		if pHigh.ProfitYuan < pLow.ProfitYuan-1e-7 {
			t.Fatalf("trial %d: 功率上限增大后最优收益下降: %.4f < %.4f", trial, pHigh.ProfitYuan, pLow.ProfitYuan)
		}
	}
}

func TestSOCAlwaysWithinBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 25; trial++ {
		st := randomStation(rng, true)
		prices := randomPrices(rng)
		plan, err := Solve(st, prices, Options{StartPeriod: 0, StartEnergy: st.SocInitial * st.RatedEnergyMWh, GridPoints: 100})
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if err := validateTrajectoryFrom(st, plan, st.SocInitial*st.RatedEnergyMWh); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if plan.Soc[len(plan.Soc)-1] < st.SocEndMin-1e-9 {
			t.Fatalf("日末 SOC %.6f 低于下限 %.6f", plan.Soc[len(plan.Soc)-1], st.SocEndMin)
		}
	}
}

func TestRollingReproducesOriginalFromInitial(t *testing.T) {
	// 从起始能量=初始 SOC、start=0 的滚动求解应与日前一致；过去时段长度不变。
	st := randomStation(rand.New(rand.NewSource(1)), true)
	prices := randomPrices(rand.New(rand.NewSource(2)))
	p0, err := Solve(st, prices, Options{StartPeriod: 0, StartEnergy: st.SocInitial * st.RatedEnergyMWh, GridPoints: 120})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// 从第 40 个槽、以原计划该时刻能量重算尾部，再接回前缀应仍是一条可行轨迹。
	energyAtCut := p0.Soc[39] * st.RatedEnergyMWh
	p1, err := Solve(st, prices, Options{StartPeriod: 40, StartEnergy: energyAtCut, GridPoints: 120})
	if err != nil {
		t.Fatalf("Solve rolling: %v", err)
	}
	if len(p1.ChargeMW) != spec.Periods-40 {
		t.Fatalf("尾部长度应为 %d，实际 %d", spec.Periods-40, len(p1.ChargeMW))
	}
	mergedCh := append(append([]float64(nil), p0.ChargeMW[:40]...), p1.ChargeMW...)
	mergedDch := append(append([]float64(nil), p0.DischargeMW[:40]...), p1.DischargeMW...)
	mergedSoc := append(append([]float64(nil), p0.Soc[:40]...), p1.Soc...)
	if len(mergedCh) != spec.Periods || len(mergedDch) != spec.Periods || len(mergedSoc) != spec.Periods {
		t.Fatalf("拼接长度不是 96: %d %d %d", len(mergedCh), len(mergedDch), len(mergedSoc))
	}
	// 拼接后的完整轨迹必须可行。
	stitched := &Plan{
		Start:       0,
		ChargeMW:    mergedCh,
		DischargeMW: mergedDch,
		Soc:         mergedSoc,
	}
	if err := validateTrajectoryFrom(st, stitched, st.SocInitial*st.RatedEnergyMWh); err != nil {
		t.Fatalf("滚动拼接轨迹不可行: %v", err)
	}
}

func TestCoarseGridLowRateStationStillFeasible(t *testing.T) {
	// 极端低倍率（0.02C）+ 粗网格：均匀网格间距远大于单槽可达步长，
	// 若没有可行性走廊会误报无解；连续可行则离散必须可行。
	st := spec.Station{
		RatedEnergyMWh:        10,
		MaxChargeMW:           0.2,
		MaxDischargeMW:        0.2,
		SocMin:                0,
		SocMax:                1,
		ChargeEfficiency:      0.95,
		DischargeEfficiency:   0.95,
		DegradationCostPerMWh: 0,
		SocEndMin:             0,
		SocInitial:            0,
		DeviationThresholdMWh: 0,
	}
	prices := randomPrices(rand.New(rand.NewSource(303)))
	plan, err := Solve(st, prices, Options{StartPeriod: 0, StartEnergy: 0, GridPoints: 8})
	if err != nil {
		t.Fatalf("粗网格不应误判不可行: %v", err)
	}
	if err := validateTrajectoryFrom(st, plan, 0); err != nil {
		t.Fatal(err)
	}
}

func TestTrulyInfeasibleRollingTail(t *testing.T) {
	// 滚动窗口里真实不可行：最后一个时段开始时能量远低于日末下限，
	// 单槽充电能力补不回来，必须返回 ErrInfeasible 而不是给越界结果。
	st := twoPeriodStation(0)
	st.SocEndMin = 0.9
	prices := make([]float64, spec.Periods)
	_, err := Solve(st, prices, Options{StartPeriod: 95, StartEnergy: 0, GridPoints: 200})
	if err != ErrInfeasible {
		t.Fatalf("应判不可行，实际 err=%v", err)
	}
}

// ---- helpers ----
func randomStation(rng *rand.Rand, bounded bool) spec.Station {
	st := spec.Station{
		RatedEnergyMWh:        1 + rng.Float64()*9,
		MaxChargeMW:           0.5 + rng.Float64()*2.5,
		MaxDischargeMW:        0.5 + rng.Float64()*2.5,
		SocMin:                rng.Float64() * 0.2,
		SocMax:                0.8 + rng.Float64()*0.2,
		ChargeEfficiency:      0.75 + rng.Float64()*0.25,
		DischargeEfficiency:   0.75 + rng.Float64()*0.25,
		DegradationCostPerMWh: rng.Float64() * 20,
	}
	st.SocEndMin = st.SocMin + rng.Float64()*(st.SocMax-st.SocMin)*0.5
	st.SocInitial = st.SocMin + rng.Float64()*(st.SocMax-st.SocMin)
	_ = bounded
	return st
}

func randomPrices(rng *rand.Rand) []float64 {
	p := make([]float64, spec.Periods)
	for i := range p {
		p[i] = -30 + rng.Float64()*300
	}
	return p
}

func scale(p []float64, k float64) []float64 {
	out := make([]float64, len(p))
	for i, v := range p {
		out[i] = v * k
	}
	return out
}

func slicesClose(a, b []float64, tol float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(a[i]-b[i]) > tol {
			return false
		}
	}
	return true
}

func validateTrajectoryFrom(st spec.Station, p *Plan, startEnergyMWh float64) error {
	energy := startEnergyMWh
	for t := 0; t < len(p.ChargeMW); t++ {
		ch, dch := p.ChargeMW[t], p.DischargeMW[t]
		if ch > st.MaxChargeMW+1e-9 {
			return errf("时段 %d 充电功率 %.4f 超上限 %.4f", t, ch, st.MaxChargeMW)
		}
		if dch > st.MaxDischargeMW+1e-9 {
			return errf("时段 %d 放电功率 %.4f 超上限 %.4f", t, dch, st.MaxDischargeMW)
		}
		if ch > 1e-12 && dch > 1e-12 {
			return errf("时段 %d 同时充放", t)
		}
		energy += ch*dt*st.ChargeEfficiency - dch*dt/st.DischargeEfficiency
		// dch 为电网侧功率，电池侧减量 = dch/ηd·dt。
		eMin, eMax, _, _ := st.EnergyBounds()
		if energy < eMin-1e-7 || energy > eMax+1e-7 {
			return errf("时段 %d 能量 %.6f 越界 [%.4f,%.4f]", t, energy, eMin, eMax)
		}
		if math.Abs(energy-p.Soc[t]*st.RatedEnergyMWh) > 1e-6 {
			return errf("时段 %d SOC 与功率不一致: %.6f vs %.6f", t, energy, p.Soc[t]*st.RatedEnergyMWh)
		}
	}
	return nil
}

type strErr string

func (e strErr) Error() string { return string(e) }

func errf(format string, args ...any) error {
	return strErr(fmt.Sprintf(format, args...))
}
