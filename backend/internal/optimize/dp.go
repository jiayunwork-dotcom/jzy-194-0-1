// Package optimize 在荷电状态（能量）离散网格上用动态规划求解日前充放计划。
//
// 决策变量：每个时段末的电池能量（MWh），能量网格上逐时段递推，阶段收益为
//
//	充电（电网取电量 c MWh，电池增量 Δe=ηc·c）：-price·c - wear·Δe
//	放电（电网收电量 d=ηd·Δe MWh）：price·d - wear·Δe
//
// wear 按电池侧吞吐 MWh 计（充、放两个方向的电池能量变化量之和）。
// 任何时段 Δe 只能为正或为负，天然保证不同时充放；idle（Δe=0）始终可选。
package optimize

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"energystorage/internal/spec"
)

// ErrInfeasible 表示从给定初始能量出发无法满足全部约束（含日末能量下限）。
var ErrInfeasible = errors.New("从当前状态无可行计划满足约束（含日末荷电状态下限）")

const dt = spec.PeriodHours

// Plan 优化结果。切片长度为 Periods-Start：Start>0 时为滚动重优化的尾部计划。
type Plan struct {
	Start       int       `json:"start_period"`
	ChargeMW    []float64 `json:"charge_mw"`
	DischargeMW []float64 `json:"discharge_mw"`
	Soc         []float64 `json:"soc"` // 各时段末 SOC（含 Start 时段）
	ProfitYuan  float64   `json:"profit_yuan"`
	GridGapMWh  float64   `json:"grid_gap_mwh"`
	ErrorBound  float64   `json:"error_bound_yuan"`
	GridPoints  int       `json:"grid_points"`
}

// Options 控制一次求解的滚动窗口。
type Options struct {
	StartPeriod int     // 起始时段 0..95，计划只包含 [StartPeriod,96)
	StartEnergy float64 // 起始时段开始时的电池能量（MWh），通常取当前遥测
	GridPoints  int     // 网格划分数；<=0 用 spec.DefaultGridPoints
}

// Solve 在给定电价与电站参数下求最优计划。
//
// startPeriod 之前的时段不参与优化（滚动重优化时已过去时段由调用方保留）。
func Solve(st spec.Station, prices []float64, opts Options) (*Plan, error) {
	if err := st.Validate(); err != nil {
		return nil, err
	}
	if err := spec.ValidatePrices(prices); err != nil {
		return nil, err
	}
	n := opts.GridPoints
	if n <= 0 {
		n = spec.DefaultGridPoints
	}
	if n < 2 {
		return nil, fmt.Errorf("grid_points 至少为 2")
	}
	start := opts.StartPeriod
	if start < 0 || start >= spec.Periods {
		return nil, fmt.Errorf("start_period 必须在 [0,%d)", spec.Periods)
	}

	emin, emax, eend, _ := st.EnergyBounds()
	e0 := opts.StartEnergy
	if e0 < emin-tolEnergy() || e0 > emax+tolEnergy() {
		return nil, fmt.Errorf("初始能量 %.6f MWh 超出 SOC 能量区间 [%.6f, %.6f]", e0, emin, emax)
	}
	e0 = clamp(e0, emin, emax)

	horizon := spec.Periods - start
	p := prices[start:]

	maxChEnergy := st.MaxChargeMW * dt * st.ChargeEfficiency        // 单时段电池最大增量
	maxDchEnergy := st.MaxDischargeMW * dt / st.DischargeEfficiency // 单时段电池最大减量

	// 能量网格：均匀网格并强制纳入边界、日末下限、初始能量，保证这些关键点无离散误差。
	// 另沿 e0→E_end 增补一条间距不超过单时段最大步长的"可行性走廊"，
	// 保证连续问题可行时离散问题必然可行（防止低倍率电站在粗网格上被误判无解）。
	grid := buildGrid(emin, emax, eend, e0, n, maxChEnergy, maxDchEnergy, horizon)
	m := len(grid)
	idx := make(map[float64]int, m+2)
	for i, e := range grid {
		idx[e] = i
	}

	// V[t][i]：从第 start+t 时段初、能量 grid[i] 出发到日末的最大净收益。
	V := make([][]float64, horizon+1)
	parent := make([][]int, horizon)
	for t := 0; t <= horizon; t++ {
		V[t] = make([]float64, m)
	}
	terminal := V[horizon]
	negInf := math.Inf(-1)
	for i, e := range grid {
		if e >= eend-tolEnergy() {
			terminal[i] = 0
		} else {
			terminal[i] = negInf
		}
	}

	for t := horizon - 1; t >= 0; t-- {
		price := p[t]
		next := V[t+1]
		cur := V[t]
		par := make([]int, m)
		for i := range cur {
			cur[i] = negInf
			par[i] = -1
		}
		for i, e := range grid {
			best := negInf
			bestJ := -1
			loE, hiE := e-maxDchEnergy, e+maxChEnergy
			jLo := sort.Search(m, func(j int) bool { return grid[j] >= loE-tolEnergy() })
			jHi := sort.Search(m, func(j int) bool { return grid[j] > hiE+tolEnergy() })
			for j := jLo; j < jHi; j++ {
				if next[j] == negInf {
					continue
				}
				r := reward(e, grid[j], price, st)
				cand := r + next[j]
				// 严格更好才替换；j 升序遍历，等值时取能量更小的目标，规则确定。
				if bestJ == -1 || cand > best+cmpTol(best) {
					best, bestJ = cand, j
				}
			}
			cur[i], par[i] = best, bestJ
		}
		parent[t] = par
	}

	startIdx := idx[roundKey(e0)]
	if V[0][startIdx] == negInf {
		return nil, ErrInfeasible
	}

	// 回溯，输出电网侧功率与时段末 SOC。
	ch := make([]float64, horizon)
	dch := make([]float64, horizon)
	soc := make([]float64, horizon)
	profit := 0.0
	cur := startIdx
	for t := 0; t < horizon; t++ {
		nxt := parent[t][cur]
		if nxt < 0 { // 理论上不可达（已由初始点可行性拦截），防御性处理。
			return nil, ErrInfeasible
		}
		eFrom, eTo := grid[cur], grid[nxt]
		r := reward(eFrom, eTo, p[t], st)
		profit += r
		switch {
		case eTo > eFrom:
			ch[t] = (eTo - eFrom) / st.ChargeEfficiency / dt
		case eTo < eFrom:
			dch[t] = (eFrom - eTo) * st.DischargeEfficiency / dt
		}
		soc[t] = eTo / st.RatedEnergyMWh
		cur = nxt
	}

	return &Plan{
		Start:       start,
		ChargeMW:    ch,
		DischargeMW: dch,
		Soc:         soc,
		ProfitYuan:  profit,
		GridGapMWh:  maxGap(grid),
		ErrorBound:  errorBound(st, p, maxGap(grid)),
		GridPoints:  m,
	}, nil
}

// reward 单时段从电池能量 eFrom 到 eTo 的净收益（元）。
func reward(eFrom, eTo, price float64, st spec.Station) float64 {
	switch {
	case eTo > eFrom:
		delta := eTo - eFrom
		gridMWh := delta / st.ChargeEfficiency // 电网取电量
		return -price*gridMWh - st.DegradationCostPerMWh*delta
	case eTo < eFrom:
		delta := eFrom - eTo
		gridMWh := delta * st.DischargeEfficiency // 电网收到电量
		return price*gridMWh - st.DegradationCostPerMWh*delta
	default:
		return 0
	}
}

// errorBound 保守的最优值误差上界（元），推导见 docs/design.md。
// 单时段值函数关于能量的 Lipschitz 常数不超过 dt·(maxp/ηc, maxp·ηd 中的较大者 + wear)，
// 每次网格吸附偏差 ≤ h；逐时段累计给出 2·N·L·h。
func errorBound(st spec.Station, prices []float64, h float64) float64 {
	maxP := 0.0
	for _, p := range prices {
		if a := math.Abs(p); a > maxP {
			maxP = a
		}
	}
	L := dt*math.Max(maxP/st.ChargeEfficiency, maxP*st.DischargeEfficiency) + dt*st.DegradationCostPerMWh
	return 2.0 * float64(len(prices)) * L * h
}

func buildGrid(emin, emax, eend, e0 float64, n int, stepCh, stepDch float64, periods int) []float64 {
	pts := make(map[float64]struct{}, n+4)
	for i := 0; i <= n; i++ {
		pts[roundKey(emin+(emax-emin)*float64(i)/float64(n))] = struct{}{}
	}
	pts[roundKey(emin)] = struct{}{}
	pts[roundKey(emax)] = struct{}{}
	pts[roundKey(clamp(eend, emin, emax))] = struct{}{}
	pts[roundKey(clamp(e0, emin, emax))] = struct{}{}

	// 可行性走廊：从 e0 单调走向 eend，等间距点数取 ⌈距离/单槽最大步长⌉，
	// 使每一跳都不超过单时段功率可达范围，且所需槽数不超过剩余时段数。
	// 这样只要连续问题可行，网格上就必然存在一条逐槽可行路径。
	step := stepCh
	from, to := e0, eend
	if to < from-tolEnergy() {
		step = stepDch
	}
	if math.Abs(to-from) > tolEnergy() && step > tolEnergy() {
		need := int(math.Ceil(math.Abs(to-from)/step - tolEnergy()))
		if need > periods {
			need = periods
		}
		for k := 0; k <= need; k++ {
			e := from + (to-from)*float64(k)/float64(need)
			pts[roundKey(clamp(e, emin, emax))] = struct{}{}
		}
	}

	out := make([]float64, 0, len(pts))
	for e := range pts {
		out = append(out, e)
	}
	sort.Float64s(out)
	return out
}

func maxGap(grid []float64) float64 {
	h := 0.0
	for i := 1; i < len(grid); i++ {
		if g := grid[i] - grid[i-1]; g > h {
			h = g
		}
	}
	return h
}

func roundKey(x float64) float64 {
	// 让同一逻辑点（如均匀网格端点与显式边界）在 map 中归并，保留足够精度。
	return math.Round(x/1e-12) * 1e-12
}

func tolEnergy() float64 { return 1e-9 }

func cmpTol(best float64) float64 { return 1e-10 * math.Max(1, math.Abs(best)) }

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// gridValue 是同一能量网格上的穷举精确值，仅供测试与 Solve 对照：
// 在完全相同的网格上逐阶段穷举所有可行状态序列，取最大净收益。
// 若从 e0 无可行序列到达满足日末下限的状态，返回 -Inf。
func gridValue(st spec.Station, prices []float64, start int, e0 float64, n int) float64 {
	if n < 2 {
		n = spec.DefaultGridPoints
	}
	emin, emax, eend, _ := st.EnergyBounds()
	horizon := spec.Periods - start
	stepCh := st.MaxChargeMW * dt * st.ChargeEfficiency
	stepDch := st.MaxDischargeMW * dt / st.DischargeEfficiency
	grid := buildGrid(emin, emax, eend, e0, n, stepCh, stepDch, horizon)
	idx := make(map[float64]int, len(grid))
	for i, e := range grid {
		idx[e] = i
	}
	startIdx := idx[roundKey(e0)]

	maxChEnergy := st.MaxChargeMW * dt * st.ChargeEfficiency
	maxDchEnergy := st.MaxDischargeMW * dt / st.DischargeEfficiency

	current := make([]float64, len(grid))
	for i := range current {
		current[i] = math.Inf(-1)
	}
	current[startIdx] = 0
	for t := 0; t < horizon; t++ {
		next := make([]float64, len(grid))
		for i := range next {
			next[i] = math.Inf(-1)
		}
		for i := range grid {
			if current[i] == math.Inf(-1) {
				continue
			}
			loE, hiE := grid[i]-maxDchEnergy, grid[i]+maxChEnergy
			for j, e := range grid {
				if e < loE-tolEnergy() || e > hiE+tolEnergy() {
					continue
				}
				cand := current[i] + reward(grid[i], e, prices[start+t], st)
				if cand > next[j] {
					next[j] = cand
				}
			}
		}
		current = next
	}
	best := math.Inf(-1)
	for i, e := range grid {
		if e >= eend-tolEnergy() && current[i] > best {
			best = current[i]
		}
	}
	return best
}
