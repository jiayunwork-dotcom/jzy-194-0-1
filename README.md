# 储能电站日前计划与滚动修正系统

工业园区储能电站的日前充放计划优化与日内滚动修正：前一天晚上根据分时电价生成
96 个 15 分钟时段的充放功率计划，运行中根据遥测在 SOC 偏差超阈值时从当前时段
到日末重新优化，旧计划版本全部保留。

- 后端：Gin + Go 1.22，PostgreSQL 16，标准 `testing`
- 前端：Vue 3 + Vite（Node.js 20 构建），Chart.js
- 求解：SOC 能量网格动态规划（方法选型、误差上界与性质证明见 [`docs/design.md`](docs/design.md)）

## 一键启动

```bash
docker compose up --build
# 打开 http://localhost:8080
```

镜像为三阶段构建：`node:20-alpine` 打前端 → `golang:1.22-alpine` 编译
（前端产物 `go:embed` 进二进制）→ `scratch` 运行层仅一个二进制；
`docker-compose.yml` 搭配 `postgres:16-alpine`。

## 本地开发

后端（需自行提供 PostgreSQL 16）：

```bash
cd backend
export DATABASE_URL='postgres://bess:bess@localhost:5432/bess?sslmode=disable'
go test ./...                       # 单元测试（不需要数据库）
go test -tags=pgint ./internal/store/   # PostgreSQL 集成测试
go run ./cmd/server
```

前端（:5173，/api 代理到 :8080）：

```bash
cd frontend
npm ci
npm run dev
```

前端发布构建后，把 `frontend/dist/` 内容拷到 `backend/web/dist/` 即嵌入后端二进制。

## 页面

1. **电站参数与电价录入**：额定能量、充放功率上限、SOC 上下限、充放效率、
   折损单价、日末 SOC 下限、初始 SOC、偏差阈值；96 点电价支持手动粘贴或
   峰平谷模板生成。非法字段红框并在提交响应中列出字段名。
2. **计划功率柱状图 + SOC 曲线**：充电向下、放电向上；计划曲线上叠加
   实际遥测点。
3. **计划版本列表**：版本号、触发原因（日前/偏差遥测/人工）、起始时段、
   优化段收益、创建时间；可回看任意历史版本，可手工触发滚动重优化。
4. **遥测录入**：遥测编号去重、时刻归属计划日校验、上报记录表。

## 关键语义

- **收益口径**：电网侧。充 1 MWh 电网电量存 `ηc` MWh；放 1 MWh 电池存量
  电网收到 `ηd` MWh；折损按电池侧吞吐 MWh 计。
- **手算例子**：1 MWh / 1 MW / η=0.9，电价 100、300：充 1、放 0.81，
  净收益 **143 元**；电价 100、120 时不动（收益 0）。测试：
  `backend/internal/optimize/dp_test.go`。
- **滚动修正**：遥测按编号幂等、按时刻归入时段；实际 SOC 与计划偏差
  （MWh）超阈值则冻结已过时段、重算尾部、存新版本并记录触发原因；
  服务重启当前版本不变（持久化在 PostgreSQL）。
- **离散误差上界**随每次计划返回 `error_bound_yuan`，默认 800 网格点；
  可在参数中调高网格划分数收紧误差。
