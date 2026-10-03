package service

import (
	"context"
	"time"
)

// Store 持久化接口。由 PostgreSQL 实现，测试中可替换为内存实现。
type Store interface {
	// LoadConfig 取当前配置；不存在返回 ErrNotFound。
	LoadConfig(ctx context.Context) (*Config, error)
	// SaveConfig 覆盖当前配置，并清空该计划日的旧计划版本与遥测（重新编排日计划）。
	SaveConfig(ctx context.Context, cfg Config) error

	// NextVersionNumber 返回 planDay 下一个版本号（从 1 开始）。
	NextVersionNumber(ctx context.Context, planDay string) (int, error)
	// InsertVersion 写入新版本，并在同一事务内把 is_current 切到新版本。
	InsertVersion(ctx context.Context, v *PlanVersion) error
	// CurrentVersion 取当前计划版本；不存在返回 ErrNotFound。
	CurrentVersion(ctx context.Context, planDay string) (*PlanVersion, error)
	// GetVersion 按版本号取历史版本。
	GetVersion(ctx context.Context, planDay string, version int) (*PlanVersion, error)
	// ListVersions 列出计划日全部版本（版本号倒序）。
	ListVersions(ctx context.Context, planDay string) ([]*PlanVersion, int, error)

	// InsertTelemetry 按遥测编号幂等写入。已存在时返回 (false,nil)。
	InsertTelemetry(ctx context.Context, t Telemetry) (bool, error)
	// ListTelemetry 返回计划日全部遥测，按时刻升序（晚到数据归位后顺序自然正确）。
	ListTelemetry(ctx context.Context, planDay string) ([]Telemetry, error)
}

// Clock 便于测试控制时间。
type Clock func() time.Time

// ErrNotFound 数据不存在。
type ErrNotFound struct{ What string }

func (e *ErrNotFound) Error() string { return e.What + " 不存在" }

// ErrConflict 当前状态下操作无法完成（如当日时段已结束、无已发生时段可冻结），
// 属业务语义错误而非服务故障（HTTP 422）。
type ErrConflict struct{ Msg string }

func (e *ErrConflict) Error() string { return e.Msg }
