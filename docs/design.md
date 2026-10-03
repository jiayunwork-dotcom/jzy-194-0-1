# 设计说明：日前充放计划与滚动修正

## 1. 问题与约定

一天 96 个 15 分钟时段，`Δt = 0.25 h`。记：

- 电池侧能量（MWh）`e_t`，SOC 为 `e_t / E`，`E` 为额定能量；
- 电网侧决策功率：充电 `c_t ≥ 0`、放电 `d_t ≥ 0`（MW），满足
  `0 ≤ c_t ≤ P_c`、`0 ≤ d_t ≤ P_d`、`c_t·d_t = 0`（不同时充放）；
- 能量平衡（单向效率 ηc、ηd ∈ (0,1]）：
  `e_{t+1} = e_t + ηc·c_t·Δt − (d_t/ηd)·Δt`；
- 允许区间 `E_min ≤ e_t ≤ E_max`，日末 `e_96 ≥ E_end`；
- 电价 `p_t`（元/MWh，允许负电价），折损单价 `w`（元/电池侧吞吐 MWh）。

单时段净收益：

- 充电：`−p_t·(c_tΔt) − w·(ηc·c_tΔt)`
- 放电：`+p_t·(d_tΔt) − w·((d_t/ηd)Δt)`
- 不动：0

目标为 96 个时段净收益之和最大。

**手算例子复核**：E=1、P=1、ηc=ηd=0.9、e0=0、w=0，两小时电价 100/300。
首小时从电网取 1 MWh（成本 100 元），存入 0.9 MWh；
次小时放出 0.9 MWh 电池存量，电网收到 0.9×0.9=0.81 MWh（收入 243 元）；
净收益 **143 元**。电价 100/120 时一充一放净得 0.81×120−100=−2.8 元，
不动为 0，故**什么都不做**。两个结论均有测试
（`TestHandExample_Profitable` / `TestHandExample_NotWorthIt`）。

## 2. 求解方法：SOC 网格动态规划

在 [SOC 能量均匀网格] 上逐时段逆序递推：

```
V_T(e)   = 0      若 e ≥ E_end，否则 −∞
V_t(e_i) = max_j [ r_t(e_i, e_j) + V_{t+1}(e_j) ]
           j 满足 e_j ∈ [e_i − (P_d/ηd)Δt, e_i + ηc P_c Δt] 且在 [E_min,E_max] 内
```

其中 `r_t` 是单时段净收益。正向回溯输出逐时段充/放功率与 SOC 轨迹。
网格额外强制纳入 `E_min、E_max、E_end、e0` 四个关键点，使边界、日末下限与
初始点无离散误差。

**为什么选网格 DP 而不是 LP**：

1. 折损按吞吐量计费在当前定价下其实是线性的，但业务上明确提到未来可能接入
   非线性折损（循环次数曲线、分档电价等）。网格 DP 的阶段收益 `r_t` 可以换成
   任意函数，递推结构不变；LP 则要求把折损线性化，精度受限。
2. 实现直接，无外部求解器依赖，单二进制部署；滚动重优化只是把起点从 0
   改成当前时段、初值改成遥测能量，同一套代码即可。
3. 约束（不同时充放、SOC 上下限、日末下限、单向效率）在状态转移层面天然满足，
   不需要互斥变量等 MIP 技巧。

代价是精度取决于网格划分数 N。N=800 时 96 时段、每状态约数百次转移，
单次求解在普通服务器上是毫秒~十毫秒量级。

### 2.1 离散误差上界

记网格最大间距 `h = (E_max−E_min)/N`（强制关键点后实际间距 ≤ h）。

对任意两个能量状态 e、e′，阶段收益差满足 Lipschitz 界：

```
|r_t(e,x) − r_t(e′,x)| ≤ L1·|e−e′|，
L1 = max(p_t/ηc, p_t·ηd)·Δt + w·Δt
```

（充电路径每单位电池增量对应 1/ηc 电网 MWh；放电路径对应 ηd；
折损按电池侧 |Δe| 计。）

逆序递推的值函数 `V_t(·)` 同样以 `L1` 为 Lipschitz 常数（取最大值保持）。
把连续最优的每一步状态吸附到相邻网格点，单点误差不超过 h/2，
沿时间正向和反向各累计一次，总目标值偏差不超过

```
|V*_h − V*| ≤ 2 · N_periods · L · h,
L = Δt·max(|p|max/ηc, |p|max·ηd) + Δt·w
```

其中 `N_periods ≤ 96`。该值随每次结果返回（`error_bound_yuan`），
N=800、E 跨度 4 MWh 时 h=0.005 MWh，对典型工业园区价格量级（千元/MWh）
误差上界在百元以内量级，且可通过增大网格划分数任意收紧。

**测试对照**（`internal/optimize/dp_test.go`）：
`TestBruteForceEquivalence` 在 60 组随机小算例上，把 DP 与
*同一网格上的逐状态序列穷举*（`gridValue`）对照，收益一致到浮点误差
（`1e-7·max(1,|V|)`）——这验证的是递推/回溯实现与穷举定义完全等价；
离散化本身的精度则由上面误差上界给出，随 N→0 收敛到连续最优。

## 3. 滚动修正与版本管理

- 遥测带唯一编号；重复编号只算一次（DB 主键幂等插入）。
- 时刻属于计划日才受理，否则 400 并指出 `timestamp` 字段；
  晚到数据按其时刻归入对应 15 分钟时段（"按时刻归位"），查询按时刻升序。
- 每条遥测比较 `|实际SOC·E − 计划SOC(该时段)·E|` 与偏差阈值。
  超阈值时：**冻结 `[0, k]` 时段**（用遥测重建实际功率/SOC，缺测点保持上一
  已知值），以第 k 时段末实际能量为起点，对 `[k+1, 96)` 重新求解。
- 新结果存为新版本，`trigger_reason` 记录触发遥测编号、时段、偏差与阈值；
  旧版本原样保留。版本切换与写入在同一事务内完成。
- 版本持久化在 PostgreSQL，服务重启只重建进程，当前版本不变
  （`TestCurrentVersionSurvivesNewStoreInstance` 用新 Service 包装同一 Store
  模拟重启）。
- 若偏差超阈但从当前状态无可行尾部计划（例如实际 SOC 已低于日末下限且剩余
  时间无法补回），遥测照常收录、接口返回 202 与告警，当前计划不被覆盖。

## 4. 要成立的性质（均有测试）

1. **价格同比缩放**：折损为 0 时全部电价乘 k>0，决策不变、收益乘 k
   （`TestPriceScalingInvariance`）。
   精确说明：目标 = 电价项（p 的一次齐次）− 折损项。**若折损非零，只缩放电价
   会改变"套利门槛"**（价差需覆盖固定折损），计划不保证不变；把电价与折损
   单价同乘 k（二者同为价格量纲）则决策不变、收益乘 k
   （`TestPriceAndWearScalingInvariance`）。这是数学事实，按精确含义实现。
2. **平价且折损非零**：非负平价、初始 SOC 等于日末下限时，不动严格最优
   （`TestFlatPriceWithWearDoesNothing`）。
   充 1 MWh 电池存量成本 `p/ηc + w`，同日同价放回收 `p·ηd − w`，
   往返严格亏损 `p(1/ηc − ηd) + 2w > 0`（ηcηd≤1、w>0、p≥0）。
   边界情形：初始 SOC 高于日末下限时，直接卖出既有存量可获正收入（非循环）；
   负平价时"充电获补贴量 > 放电售出量"的量差也可能盈利。二者不是"循环套利"，
   属题设之外，系统不予禁止（支持负电价）。
3. **SOC 轨迹不越界**：所有输出时段末 SOC ∈ [soc_min, soc_max]、
   日末 ≥ soc_end_min、功率不超限、不同时充放
   （`TestSOCAlwaysWithinBounds` 与每例轨迹校验）。
4. **放松功率上限最优收益不下降**：`(P_c, P_d)` 增大后可行域包含原可行域
   （`TestMorePowerNeverReducesProfit`）。
5. **校验拒收并指出字段**（`spec` 包测试 + API 测试）：
   效率 ∉ (0,1]、soc_min ≥ soc_max、功率/能量为负、电价数量 ≠ 96、
   遥测时刻不属于计划日，均返回 400 且 `fields` 列出字段名。

## 5. 架构

```
frontend/ (Vue 3 + Vite, Chart.js)
  ConfigForm   电站参数 + 96 点电价录入（支持峰平谷一键填充）
  PlanChart    计划充放功率柱状图（充↓放↑）+ SOC 曲线，叠加实际遥测
  VersionList  版本列表、手工触发滚动
  TelemetryPanel 遥测录入与上报记录
backend/ (Gin, Go 1.22)
  cmd/server     启动、优雅关闭
  internal/api   HTTP/JSON，字段级 400，SPA 静态回退
  internal/service  业务编排：配置、版本、遥测去重/归位/偏差/重优化
  internal/optimize 网格 DP（纯函数，无 IO，易测）
  internal/spec     参数与电价校验
  internal/store    PostgreSQL 实现（database/sql + pgx，迁移 SQL 内嵌）
  web/dist          前端产物 go:embed 进二进制
```

存储三张表：`config`（当前参数与电价，单行）、`plans`（版本全量保留，
冻结前缀与优化尾部分列）、`telemetry`（编号主键，时刻索引）。

## 6. HTTP 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/api/config` | 读取 / 录入参数电价（POST 同时生成 v1） |
| GET | `/api/plans?day=YYYY-MM-DD` | 版本列表 + 当前版本号 |
| GET | `/api/plans/current?day=` | 当前版本完整 96 点 |
| GET | `/api/plans/:version?day=` | 历史版本 |
| POST | `/api/reoptimize` | 手工滚动重优化 |
| POST | `/api/telemetry` | 上报遥测（幂等，可能触发新版本） |
| GET | `/api/telemetry?day=` | 遥测列表（时刻升序） |

## 7. 部署

多阶段镜像：`node:20-alpine` 构建前端 → `golang:1.22-alpine` 编译
（前端产物嵌入二进制，`-trimpath -ldflags="-s -w"`）→ `scratch` 运行层
仅一个二进制；`docker-compose.yml` 搭配 `postgres:16-alpine`，
健康检查通过后再启动应用。
