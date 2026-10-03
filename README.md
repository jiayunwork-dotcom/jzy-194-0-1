# 储能电站充放计划系统

面向工业园区储能电站的峰谷套利调度系统：前一天根据 96 时段分时电价生成日前
充放计划；运行当天接收遥测（实际 SoC/功率），偏差超阈值时从当前时段到日末
滚动重排，所有计划版本留痕。

- 后端：Go 1.22 + Gin，PostgreSQL 16（pgx/v5），标准 `testing` 测试
- 前端：Vue 3 + Vite + ECharts（Node 20 构建）
- 求解：SoC 网格动态规划（选择理由与误差上界推导见 [docs/DESIGN.md](docs/DESIGN.md)）

## 快速开始（Docker）

```bash
docker compose up --build
# 打开 http://localhost:8080
```

依次：「参数与电价」录入电站参数和次日电价 → 「生成日前计划」→
「运行计划」查看计划功率柱状图、SoC 曲线与实际遥测叠加 → 「计划版本」查看版本历史。

## 本地开发

后端（需要 Go 1.22）：

```bash
cd backend
go test ./...                 # 全部测试（PG 集成测试无数据库时自动跳过）
STORE=memory go run ./cmd/server   # 内存存储演示模式，无需数据库，:8080
```

前端（需要 Node 20）：

```bash
cd frontend
npm install
npm run dev                   # :5173，/api 代理到 :8080
```

PostgreSQL 集成测试（可选）：

```bash
docker compose up -d db
cd backend
TEST_DATABASE_URL="postgres://energy:energy@localhost:5432/energy?sslmode=disable" \
  go test ./internal/store/pg/
```

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `PORT` | `8080` | 监听端口 |
| `DATABASE_URL` | — | PostgreSQL 连接串（`STORE=pg` 时必填） |
| `STORE` | `pg` | `pg` 或 `memory`（演示用，重启丢数据） |
| `STATIC_DIR` | `./static` | 前端静态文件目录（镜像内为 `/static`） |
| `PLAN_TZ` | `Asia/Shanghai` | 计划日所属时区 |
| `GRID_POINTS` | `10000` | SoC 网格段数（精度/速度权衡，见设计文档） |

## 目录结构

```
├── Dockerfile            # 多阶段：node:20-alpine 前端 → golang:1.22-alpine 后端 → 运行层
├── docker-compose.yml    # app + postgres:16-alpine
├── docs/DESIGN.md        # 模型、求解方法选择、误差上界、测试策略、滚动修正规则
├── backend/
│   ├── cmd/server/       # 入口（环境变量、优雅退出）
│   └── internal/
│       ├── optimize/     # SoC 网格 DP 求解器（+ 穷举对拍/性质测试）
│       ├── core/         # 领域类型与校验
│       ├── service/      # 业务逻辑：日前优化、遥测幂等、滚动重排
│       ├── store/        # 存储接口 + memory 实现 + pg 实现
│       └── api/          # Gin 路由与错误映射
└── frontend/             # Vue 3 + Vite + ECharts
```

## API 摘要

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/PUT | `/api/station` | 电站参数 |
| GET/PUT | `/api/prices/:date` | 96 时段电价 |
| POST | `/api/plans/:date/optimize` | 日前优化 `{initial_soc}`，生成新版本 |
| GET | `/api/plans/:date` | 当前计划版本 |
| GET | `/api/plans/:date/versions` | 版本列表 |
| GET | `/api/plans/:date/versions/:v` | 指定版本 |
| POST | `/api/plans/:date/telemetry` | 遥测上报（按 `id` 幂等） |
| GET | `/api/plans/:date/telemetry` | 遥测列表（按时刻归位） |

校验失败返回 400 并指出字段：`{"errors":[{"field":"charge_eff","message":"..."}]}`；
优化不可行返回 422；资源不存在返回 404。
