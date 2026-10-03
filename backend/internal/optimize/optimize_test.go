package optimize_test

import (
	"math"
	"math/rand"
	"testing"

	"essplanner/internal/optimize"
)

// ---------- 手算算例（来自需求文档） ----------

func handParams() optimize.Params {
	return optimize.Params{
		EnergyMWh: 1, MaxChargeMW: 1, MaxDischargeMW: 1,
		SocMin: 0, SocMax: 1,
		ChargeEff: 0.9, DischargeEff: 0.9,
		DegradationPerMWh: 0, EndSocMin: 0,
		SlotHours: 1, GridPoints: 1000,
	}
}

// 电价 100/300：第一时段充 1 MWh，第二时段放出 0.81 MWh，净收益 143 元。
func TestHandExampleArbitrage(t *testing.T) {
	slots, rev, err := optimize.Optimize([]float64{100, 300}, handParams(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(rev-143) > 1e-6 {
		t.Fatalf("收益 = %v, 期望 143", rev)
	}
	if math.Abs(slots[0].PowerMW-(-1)) > 1e-9 {
		t.Fatalf("时段0功率 = %v, 期望 -1（充电 1 MW）", slots[0].PowerMW)
	}
	if math.Abs(slots[1].PowerMW-0.81) > 1e-9 {
		t.Fatalf("时段1功率 = %v, 期望 0.81（放电 0.81 MW）", slots[1].PowerMW)
	}
	if math.Abs(slots[0].SocMWh-0.9) > 1e-9 || math.Abs(slots[1].SocMWh) > 1e-9 {
		t.Fatalf("SoC 轨迹异常: %+v", slots)
	}
}

// 电价 100/120：价差盖不住效率损失，最优是什么都不做。
func TestHandExampleNoTrade(t *testing.T) {
	slots, rev, err := optimize.Optimize([]float64{100, 120}, handParams(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(rev) > 1e-9 {
		t.Fatalf("收益 = %v, 期望 0", rev)
	}
	for i, s := range slots {
		if s.PowerMW != 0 {
			t.Fatalf("时段%d功率 = %v, 期望 0", i, s.PowerMW)
		}
	}
}

// ---------- 穷举对拍 ----------

// bruteForce 在与 DP 完全相同的网格上穷举所有可行轨迹（无记忆化，独立实现）。
func bruteForce(prices []float64, p optimize.Params, sInit float64) float64 {
	grid := optimize.BuildGrid(p, sInit)
	aC, aD := optimize.StepLimits(p)
	eps := 1e-9 * math.Max(1, math.Max(aC, aD))
	eEnd := p.EndSocMin * p.EnergyMWh
	T := len(prices)
	var rec func(t, gi int) float64
	rec = func(t, gi int) float64 {
		if t == T {
			if grid[gi] >= eEnd-1e-9 {
				return 0
			}
			return math.Inf(-1)
		}
		best := math.Inf(-1)
		for j := 0; j < len(grid); j++ {
			d := grid[j] - grid[gi]
			var v float64
			switch {
			case d > 0:
				if d > aC+eps {
					continue
				}
				v = -(prices[t]/p.ChargeEff + p.DegradationPerMWh) * d
			case d < 0:
				if -d > aD+eps {
					continue
				}
				v = (prices[t]*p.DischargeEff - p.DegradationPerMWh) * (-d)
			}
			r := rec(t+1, j)
			if math.IsInf(r, -1) {
				continue
			}
			if x := v + r; x > best {
				best = x
			}
		}
		return best
	}
	// 找起始 SoC 所在网格点
	start := 0
	for i, g := range grid {
		if math.Abs(g-sInit) < math.Abs(grid[start]-sInit) {
			start = i
		}
	}
	return rec(0, start)
}

func randomParams(r *rand.Rand, gridPoints int) optimize.Params {
	socMin := r.Float64() * 0.3
	socMax := 0.7 + r.Float64()*0.3
	return optimize.Params{
		EnergyMWh:         0.5 + r.Float64()*2,
		MaxChargeMW:       0.2 + r.Float64()*1.3,
		MaxDischargeMW:    0.2 + r.Float64()*1.3,
		SocMin:            socMin,
		SocMax:            socMax,
		ChargeEff:         0.6 + r.Float64()*0.4,
		DischargeEff:      0.6 + r.Float64()*0.4,
		DegradationPerMWh: r.Float64() * 30,
		EndSocMin:         r.Float64() * socMax,
		SlotHours:         0.25,
		GridPoints:        gridPoints,
	}
}

func randomPrices(r *rand.Rand, n int) []float64 {
	prices := make([]float64, n)
	for i := range prices {
		prices[i] = -50 + r.Float64()*550 // 含负电价
	}
	return prices
}

// 随机小算例：DP 结果必须与同一网格上的穷举结果一致（允许 1e-6 相对误差，
// 仅用于吸收浮点求和顺序差异；DP 在网格上是精确的）。
func TestAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for kase := 0; kase < 60; kase++ {
		p := randomParams(r, 6+r.Intn(8))
		T := 2 + r.Intn(3) // 2..4 个时段
		prices := randomPrices(r, T)
		sInit := (p.SocMin + r.Float64()*(p.SocMax-p.SocMin)) * p.EnergyMWh

		slots, rev, err := optimize.Optimize(prices, p, sInit)
		bf := bruteForce(prices, p, sInit)
		if math.IsInf(bf, -1) {
			if err != optimize.ErrInfeasible {
				t.Fatalf("case %d: 穷举不可行但 DP 未报错 (rev=%v, err=%v)", kase, rev, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("case %d: DP 报错 %v，穷举最优 %v", kase, err, bf)
		}
		if tol := 1e-6 * math.Max(1, math.Abs(bf)); math.Abs(rev-bf) > tol {
			t.Fatalf("case %d: DP 收益 %v != 穷举 %v (params=%+v prices=%v sInit=%v)",
				kase, rev, bf, p, prices, sInit)
		}
		checkTrajectory(t, kase, slots, prices, p, sInit, rev)
	}
}

// checkTrajectory 独立复核 DP 输出的轨迹：可行性 + 收益自洽。
func checkTrajectory(t *testing.T, kase int, slots []optimize.SlotResult, prices []float64, p optimize.Params, sInit, rev float64) {
	t.Helper()
	aC, aD := optimize.StepLimits(p)
	eMin, eMax := p.SocMin*p.EnergyMWh, p.SocMax*p.EnergyMWh
	tol := 1e-6 * math.Max(1, p.EnergyMWh)
	soc := sInit
	sum := 0.0
	for i, s := range slots {
		d := s.SocMWh - soc
		if d > aC+tol || -d > aD+tol {
			t.Fatalf("case %d 时段%d: 动作 %v MWh 超出功率约束", kase, i, d)
		}
		if s.SocMWh < eMin-tol || s.SocMWh > eMax+tol {
			t.Fatalf("case %d 时段%d: SoC %v 越出 [%v, %v]", kase, i, s.SocMWh, eMin, eMax)
		}
		// 功率与 SoC 变动方向一致，且不超上限
		if d > tol && (s.PowerMW >= 0 || -s.PowerMW > p.MaxChargeMW+1e-6) {
			t.Fatalf("case %d 时段%d: 充电功率 %v 异常", kase, i, s.PowerMW)
		}
		if d < -tol && (s.PowerMW <= 0 || s.PowerMW > p.MaxDischargeMW+1e-6) {
			t.Fatalf("case %d 时段%d: 放电功率 %v 异常", kase, i, s.PowerMW)
		}
		if d > 0 {
			sum -= (prices[i]/p.ChargeEff + p.DegradationPerMWh) * d
		} else {
			sum += (prices[i]*p.DischargeEff - p.DegradationPerMWh) * (-d)
		}
		soc = s.SocMWh
	}
	if soc < p.EndSocMin*p.EnergyMWh-tol {
		t.Fatalf("case %d: 日末 SoC %v 低于下限 %v", kase, soc, p.EndSocMin*p.EnergyMWh)
	}
	if math.Abs(sum-rev) > 1e-6*math.Max(1, math.Abs(rev)) {
		t.Fatalf("case %d: 报告收益 %v 与轨迹重算 %v 不一致", kase, rev, sum)
	}
}

// ---------- 离散化误差界 ----------

// 同一算例，细网格（8N）与粗网格（N）最优值之差不得超过文档给出的误差上界；
// 且细网格包含粗网格全部节点，最优值不得更小。
func TestGridConvergenceBound(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for kase := 0; kase < 25; kase++ {
		coarse := randomParams(r, 10+r.Intn(10))
		fine := coarse
		fine.GridPoints = coarse.GridPoints * 8
		prices := randomPrices(r, 6)
		sInit := (coarse.SocMin + r.Float64()*(coarse.SocMax-coarse.SocMin)) * coarse.EnergyMWh

		_, revC, errC := optimize.Optimize(prices, coarse, sInit)
		_, revF, errF := optimize.Optimize(prices, fine, sInit)
		if errC == optimize.ErrInfeasible || errF == optimize.ErrInfeasible {
			continue
		}
		if errC != nil || errF != nil {
			t.Fatal(errC, errF)
		}
		if revF < revC-1e-6*math.Max(1, math.Abs(revC)) {
			t.Fatalf("case %d: 细网格收益 %v < 粗网格 %v", kase, revF, revC)
		}
		bound := optimize.ErrorBound(prices, coarse)
		if revF-revC > bound*1.001+1e-9 {
			t.Fatalf("case %d: 网格误差 %v 超过上界 %v", kase, revF-revC, bound)
		}
	}
}

// ---------- 不变量性质 ----------

// 所有电价乘正数 α（折损为 0 时）：计划不变，收益同比例放大。
// 折损不为 0 时，把折损也同乘 α，性质同样成立（目标函数整体齐次）。
func TestPriceScalingInvariance(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	const alpha = 2.7
	for kase := 0; kase < 30; kase++ {
		p := randomParams(r, 200)
		p.DegradationPerMWh = 0
		prices := randomPrices(r, 8)
		sInit := (p.SocMin + r.Float64()*(p.SocMax-p.SocMin)) * p.EnergyMWh

		scaled := make([]float64, len(prices))
		for i, v := range prices {
			scaled[i] = alpha * v
		}
		slots1, rev1, err1 := optimize.Optimize(prices, p, sInit)
		slots2, rev2, err2 := optimize.Optimize(scaled, p, sInit)
		if err1 == optimize.ErrInfeasible || err2 == optimize.ErrInfeasible {
			continue
		}
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		if math.Abs(rev2-alpha*rev1) > 1e-6*math.Max(1, math.Abs(alpha*rev1)) {
			t.Fatalf("case %d: 收益未同比例放大: %v vs %v*%v", kase, rev2, rev1, alpha)
		}
		for i := range slots1 {
			if math.Abs(slots1[i].PowerMW-slots2[i].PowerMW) > 1e-9 ||
				math.Abs(slots1[i].SocMWh-slots2[i].SocMWh) > 1e-9 {
				t.Fatalf("case %d 时段%d: 计划改变: %+v vs %+v", kase, i, slots1[i], slots2[i])
			}
		}
	}
}

// 电价全天相同且折损不为零：最优是不动。
// 前提：起始 SoC 等于日末下限（否则变现库存电量本身就是最优，不属于该性质）。
func TestFlatPriceWithDegradation(t *testing.T) {
	p := handParams()
	p.DegradationPerMWh = 5
	p.EndSocMin = 0.2
	prices := make([]float64, 96)
	for i := range prices {
		prices[i] = 200
	}
	for _, init := range []float64{0.2, 0.5, 1.0} {
		p.EndSocMin = init // 日末下限 = 起始 SoC，无库存可变现
		slots, rev, err := optimize.Optimize(prices, p, init*p.EnergyMWh)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(rev) > 1e-9 {
			t.Fatalf("init=%v: 平坦电价+折损下收益 = %v, 期望 0", init, rev)
		}
		for i, s := range slots {
			if s.PowerMW != 0 {
				t.Fatalf("init=%v 时段%d 功率 = %v, 期望不动", init, i, s.PowerMW)
			}
		}
	}
}

// SoC 轨迹始终不越上下限，日末不低于下限（随机算例批量复核）。
func TestSocAlwaysWithinBounds(t *testing.T) {
	r := rand.New(rand.NewSource(13))
	for kase := 0; kase < 40; kase++ {
		p := randomParams(r, 100)
		prices := randomPrices(r, 12)
		sInit := (p.SocMin + r.Float64()*(p.SocMax-p.SocMin)) * p.EnergyMWh
		slots, rev, err := optimize.Optimize(prices, p, sInit)
		if err == optimize.ErrInfeasible {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		checkTrajectory(t, kase, slots, prices, p, sInit, rev)
	}
}

// 把功率上限调大，最优收益不下降。
func TestPowerLimitMonotonicity(t *testing.T) {
	r := rand.New(rand.NewSource(17))
	for kase := 0; kase < 30; kase++ {
		p := randomParams(r, 200)
		prices := randomPrices(r, 8)
		sInit := (p.SocMin + r.Float64()*(p.SocMax-p.SocMin)) * p.EnergyMWh

		bigger := p
		bigger.MaxChargeMW = p.MaxChargeMW*1.4 + 0.05
		bigger.MaxDischargeMW = p.MaxDischargeMW*1.4 + 0.05

		_, rev1, err1 := optimize.Optimize(prices, p, sInit)
		_, rev2, err2 := optimize.Optimize(prices, bigger, sInit)
		if err1 == optimize.ErrInfeasible || err2 == optimize.ErrInfeasible {
			continue
		}
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		if rev2 < rev1-1e-6*math.Max(1, math.Abs(rev1)) {
			t.Fatalf("case %d: 功率上限调大后收益 %v < %v", kase, rev2, rev1)
		}
	}
}

// 起始 SoC 越出上下限（实际运行可能发生）：首时段回到界内。
func TestOutOfBoundsInitialSoc(t *testing.T) {
	p := handParams()
	p.SocMin, p.SocMax = 0.2, 0.9
	p.EndSocMin = 0.2
	prices := []float64{50, 50, 50}
	slots, _, err := optimize.Optimize(prices, p, 0.05) // 低于 soc_min
	if err != nil {
		t.Fatal(err)
	}
	if slots[0].SocMWh < 0.2-1e-9 {
		t.Fatalf("首时段末 SoC %v 未回到下限 0.2 之上", slots[0].SocMWh)
	}
	if slots[0].PowerMW >= 0 {
		t.Fatalf("应充电回到界内, 功率 = %v", slots[0].PowerMW)
	}
	// 一个时段回不去：不可行
	p.MaxChargeMW = 0.01
	if _, _, err := optimize.Optimize(prices, p, 0.0); err != optimize.ErrInfeasible {
		t.Fatalf("期望 ErrInfeasible, 得到 %v", err)
	}
}
