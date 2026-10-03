// Package store 定义持久化接口。生产实现为 PostgreSQL（store/pg），
// 内存实现（store/memory）用于测试与无数据库的本地演示。
package store

import (
	"context"
	"errors"

	"essplanner/internal/core"
)

// ErrNotFound 记录不存在。
var ErrNotFound = errors.New("记录不存在")

// Store 持久化接口。所有状态（含当前计划版本）都落在实现里，
// 服务重启后当前计划版本不变。
type Store interface {
	GetStation(ctx context.Context) (*core.StationParams, error)
	PutStation(ctx context.Context, p *core.StationParams) error

	GetPrices(ctx context.Context, date string) ([]float64, error)
	PutPrices(ctx context.Context, date string, prices []float64) error

	// CreatePlan 原子分配下一版本号（date 内 max(version)+1）并写入，
	// 返回带版本号与创建时间的完整 Plan。
	CreatePlan(ctx context.Context, plan *core.Plan) (*core.Plan, error)
	// GetCurrentPlan 返回 date 的当前版本（版本号最大者）。
	GetCurrentPlan(ctx context.Context, date string) (*core.Plan, error)
	GetPlanVersion(ctx context.Context, date string, version int) (*core.Plan, error)
	ListPlanVersions(ctx context.Context, date string) ([]core.PlanMeta, error)

	// InsertTelemetry 按遥测编号幂等写入；已存在返回 inserted=false。
	InsertTelemetry(ctx context.Context, t *core.Telemetry) (inserted bool, err error)
	// LatestTelemetry 返回采集时刻最新的一条（晚到的旧遥测不算）。
	LatestTelemetry(ctx context.Context, date string) (*core.Telemetry, error)
	ListTelemetry(ctx context.Context, date string) ([]core.Telemetry, error)

	Close()
}
