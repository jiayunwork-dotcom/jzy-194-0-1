// Package optimize 实现储能电站日前/滚动充放计划的求解。
//
// 方法：荷电状态（SoC）网格上的动态规划（后向归纳）。
// 选择该方法而非线性规划的理由，以及离散化误差上界的推导，见 docs/DESIGN.md。
//
// 模型约定：
//   - 充电：从电网取 P MW，电池侧每时段增加 ChargeEff·P·SlotHours MWh；
//   - 放电：电池侧每时段减少 P·SlotHours/DischargeEff MWh，电网收到 P MW；
//   - 折损成本按电池侧吞吐量计：每存入或放出 1 MWh 收 DegradationPerMWh 元；
//   - 单时段只取一个目标 SoC，因此结构上不可能同时充放；
//   - 目标：净收益 = 放电收入 − 充电成本 − 折损成本 最大。
package optimize

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Params 求解所需的电站参数（调用前须已校验）。
type Params struct {
	EnergyMWh         float64 // 额定能量 MWh
	MaxChargeMW       float64 // 最大充电功率 MW（从电网取电侧）
	MaxDischargeMW    float64 // 最大放电功率 MW（向电网送电侧）
	SocMin            float64 // 荷电状态下限，0..1
	SocMax            float64 // 荷电状态上限，0..1
	ChargeEff         float64 // 充电效率 (0,1]
	DischargeEff      float64 // 放电效率 (0,1]
	DegradationPerMWh float64 // 每 MWh 电池侧吞吐量的折损成本，元/MWh
	EndSocMin         float64 // 日末荷电状态下限，0..1
	SlotHours         float64 // 单时段时长（小时），15 分钟 = 0.25
	GridPoints        int     // SoC 网格段数
}

// SlotResult 单时段计划。
type SlotResult struct {
	PowerMW float64 // 正=放电，负=充电，0=不动
	SocMWh  float64 // 时段末 SoC（MWh）
}

// ErrInfeasible 表示在给定约束下不存在满足日末 SoC 下限的可行轨迹。
var ErrInfeasible = errors.New("optimize: 无可行轨迹（日末荷电状态约束不可达）")

var negInf = math.Inf(-1)

// BuildGrid 构造 SoC 网格（MWh，升序）：[SocMin,SocMax]·Energy 的 GridPoints 等分，
// 再精确并入起始 SoC（若在界内）与日末下限，消除端点取整误差。导出供测试复用。
func BuildGrid(p Params, sInitMWh float64) []float64 {
	n := p.GridPoints
	if n < 1 {
		n = 1
	}
	eMin := p.SocMin * p.EnergyMWh
	eMax := p.SocMax * p.EnergyMWh
	eEnd := p.EndSocMin * p.EnergyMWh
	delta := (eMax - eMin) / float64(n)
	grid := make([]float64, 0, n+3)
	for i := 0; i <= n; i++ {
		grid = append(grid, eMin+float64(i)*delta)
	}
	if sInitMWh >= eMin && sInitMWh <= eMax {
		grid = append(grid, sInitMWh)
	}
	if eEnd > eMin && eEnd < eMax {
		grid = append(grid, eEnd)
	}
	sort.Float64s(grid)
	return dedup(grid)
}

// StepLimits 返回单时段电池侧 SoC 变动上限（MWh）：aC 充电增加上限，aD 放电减少上限。
func StepLimits(p Params) (aC, aD float64) {
	return p.ChargeEff * p.MaxChargeMW * p.SlotHours,
		p.MaxDischargeMW * p.SlotHours / p.DischargeEff
}

// ErrorBound 返回离散化误差上界（元）：设 V* 为连续最优值、V_grid 为网格 DP 最优值，
// 则 0 ≤ V* − V_grid ≤ ErrorBound。推导见 docs/DESIGN.md。
func ErrorBound(prices []float64, p Params) float64 {
	aC, aD := StepLimits(p)
	a := math.Min(aC, aD) // 单时段电池侧变动下限（用于缩放论证）
	aHat := math.Max(aC, aD)
	if a <= 0 {
		if aHat <= 0 {
			return 0 // 无法动作，DP 精确
		}
		a = aHat // 只有一个方向可动，按该方向论证
	}
	c := (p.SocMax - p.SocMin) * p.EnergyMWh
	n := p.GridPoints
	if n < 1 {
		n = 1
	}
	delta := c / float64(n)
	maxAbsPrice := 0.0
	for _, pr := range prices {
		if math.Abs(pr) > maxAbsPrice {
			maxAbsPrice = math.Abs(pr)
		}
	}
	etaMin := math.Min(p.ChargeEff, p.DischargeEff)
	m := maxAbsPrice/etaMin + p.DegradationPerMWh // 单位电池侧能量的最大边际价值
	t := float64(len(prices))
	return m * delta * (2*t*aHat/a + c/a)
}

// Optimize 求 len(prices) 个时段的最优充放计划。sInitMWh 为起始 SoC：
//   - 在 [SocMin,SocMax]·Energy 内：精确加入网格；
//   - 在 [0,Energy] 内但越出上下限（实际运行可能发生）：首时段必须回到界内，
//     一个时段回不去则返回 ErrInfeasible。
//
// 返回各时段计划与净收益（元）。
func Optimize(prices []float64, p Params, sInitMWh float64) ([]SlotResult, float64, error) {
	T := len(prices)
	if T == 0 {
		return nil, 0, nil
	}
	if sInitMWh < 0 || sInitMWh > p.EnergyMWh {
		return nil, 0, fmt.Errorf("optimize: 起始 SoC %.6f MWh 超出 [0, %.6f]", sInitMWh, p.EnergyMWh)
	}
	eMin := p.SocMin * p.EnergyMWh
	eMax := p.SocMax * p.EnergyMWh
	eEnd := p.EndSocMin * p.EnergyMWh
	grid := BuildGrid(p, sInitMWh)
	m := len(grid)
	aC, aD := StepLimits(p)
	eps := 1e-9 * math.Max(1, math.Max(aC, aD))

	// 终端值：日末 SoC 不低于 eEnd
	V := make([]float64, m)
	endTol := 1e-9 * math.Max(1, math.Abs(eEnd))
	for j := 0; j < m; j++ {
		if grid[j] >= eEnd-endTol {
			V[j] = 0
		} else {
			V[j] = negInf
		}
	}

	policy := make([][]int32, T)
	next := make([]float64, m)
	GC := make([]float64, m)
	GD := make([]float64, m)
	chgMax := make([]float64, m)
	chgArg := make([]int, m)
	disMax := make([]float64, m)
	disArg := make([]int, m)
	var V1 []float64 // 起始 SoC 在网格外时，首时段特判需要 V_1

	for t := T - 1; t >= 0; t-- {
		chargeCost := prices[t]/p.ChargeEff + p.DegradationPerMWh       // 每存入 1 MWh 的净成本
		dischargeGain := prices[t]*p.DischargeEff - p.DegradationPerMWh // 每放出 1 MWh 的净收益
		for j := 0; j < m; j++ {
			GC[j] = V[j] - chargeCost*grid[j]
			GD[j] = V[j] - dischargeGain*grid[j]
		}
		// 充电：从 i 到 j∈(i, hi(i)]，价值 = GC[j] + chargeCost·grid[i]。
		// 双指针 + 单调队列求滑动窗口最大值。
		dq := make([]int, 0, 64)
		r := 0
		for i := 0; i < m; i++ {
			if r < i {
				r = i
			}
			for r+1 < m && grid[r+1]-grid[i] <= aC+eps {
				r++
				for len(dq) > 0 && GC[dq[len(dq)-1]] <= GC[r] {
					dq = dq[:len(dq)-1]
				}
				dq = append(dq, r)
			}
			for len(dq) > 0 && dq[0] <= i {
				dq = dq[1:]
			}
			if len(dq) > 0 {
				chgMax[i] = GC[dq[0]]
				chgArg[i] = dq[0]
			} else {
				chgMax[i] = negInf
				chgArg[i] = -1
			}
		}
		// 放电：从 i 到 j∈[lo(i), i)，价值 = GD[j] + dischargeGain·grid[i]。
		dq = dq[:0]
		for i := 0; i < m; i++ {
			if i > 0 {
				j := i - 1
				for len(dq) > 0 && GD[dq[len(dq)-1]] <= GD[j] {
					dq = dq[:len(dq)-1]
				}
				dq = append(dq, j)
			}
			for len(dq) > 0 && grid[i]-grid[dq[0]] > aD+eps {
				dq = dq[1:]
			}
			if len(dq) > 0 {
				disMax[i] = GD[dq[0]]
				disArg[i] = dq[0]
			} else {
				disMax[i] = negInf
				disArg[i] = -1
			}
		}
		if t == 0 {
			V1 = append([]float64(nil), V...)
		}
		pol := make([]int32, m)
		for i := 0; i < m; i++ {
			best := V[i] // 不动
			bestJ := i
			tol := 1e-9 * (1 + math.Abs(best)) // 严格更优才切换：平局偏向不动
			if math.IsInf(best, -1) {
				tol = 1e-9
			}
			if chgArg[i] >= 0 {
				if v := chgMax[i] + chargeCost*grid[i]; v > best+tol {
					best = v
					bestJ = chgArg[i]
				}
			}
			if disArg[i] >= 0 {
				if v := disMax[i] + dischargeGain*grid[i]; v > best+tol {
					best = v
					bestJ = disArg[i]
				}
			}
			next[i] = best
			pol[i] = int32(bestJ)
		}
		policy[t] = pol
		V, next = next, V
	}

	slots := make([]SlotResult, T)
	revenue := 0.0
	startT := 0
	cur := 0

	if sInitMWh >= eMin && sInitMWh <= eMax {
		// 起始 SoC 在网格上（BuildGrid 已并入精确点）
		cur = nearestIndex(grid, sInitMWh)
		if V[cur] <= negInf/2 {
			return nil, 0, ErrInfeasible
		}
	} else {
		// 起始 SoC 越界（仅实际运行中可能发生）：首时段必须回到界内
		best := negInf
		bestJ := -1
		for j := 0; j < m; j++ {
			d := grid[j] - sInitMWh
			if d > aC+eps || -d > aD+eps {
				continue
			}
			if V1[j] <= negInf/2 {
				continue
			}
			if v := phi(prices[0], d, p) + V1[j]; v > best {
				best = v
				bestJ = j
			}
		}
		if bestJ < 0 {
			return nil, 0, ErrInfeasible
		}
		d := grid[bestJ] - sInitMWh
		slots[0] = SlotResult{PowerMW: powerOf(d, p), SocMWh: grid[bestJ]}
		revenue += phi(prices[0], d, p)
		cur = bestJ
		startT = 1
	}

	for t := startT; t < T; t++ {
		j := int(policy[t][cur])
		d := grid[j] - grid[cur]
		slots[t] = SlotResult{PowerMW: powerOf(d, p), SocMWh: grid[j]}
		revenue += phi(prices[t], d, p)
		cur = j
	}
	return slots, revenue, nil
}

// phi 单时段净收益：d 为电池侧 SoC 变动（MWh，正=充电）。
func phi(price, d float64, p Params) float64 {
	if d > 0 {
		return -(price/p.ChargeEff + p.DegradationPerMWh) * d
	}
	return (price*p.DischargeEff - p.DegradationPerMWh) * (-d)
}

// powerOf 由电池侧变动 d（MWh）换算计划功率（MW，正=放电）。
func powerOf(d float64, p Params) float64 {
	if d > 0 {
		return -d / (p.ChargeEff * p.SlotHours)
	}
	if d < 0 {
		return -d * p.DischargeEff / p.SlotHours
	}
	return 0
}

func nearestIndex(grid []float64, x float64) int {
	i := sort.Search(len(grid), func(k int) bool { return grid[k] >= x })
	if i >= len(grid) {
		return len(grid) - 1
	}
	if i > 0 && x-grid[i-1] < grid[i]-x {
		return i - 1
	}
	return i
}

func dedup(xs []float64) []float64 {
	out := xs[:1]
	for _, x := range xs[1:] {
		if x-out[len(out)-1] > 1e-9*math.Max(1, math.Abs(x)) {
			out = append(out, x)
		}
	}
	return out
}
