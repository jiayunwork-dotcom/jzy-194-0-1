// Package store 是 service.Store 的 PostgreSQL 16 实现。
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"energystorage/internal/service"
)

//go:embed migrations/0001_init.sql
var migrationSQL string

// PG PostgreSQL 存储。
type PG struct {
	db *sql.DB
}

// Open 连接数据库并执行迁移。
func Open(ctx context.Context, dsn string) (*PG, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	p := &PG{db: db}
	if err := p.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return p, nil
}

// Close 关闭连接池。
func (p *PG) Close() error { return p.db.Close() }

func (p *PG) migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, migrationSQL)
	return err
}

// LoadConfig 实现 service.Store。
func (p *PG) LoadConfig(ctx context.Context) (*service.Config, error) {
	var stationRaw, pricesRaw []byte
	var day time.Time
	err := p.db.QueryRowContext(ctx,
		`SELECT station_json, prices_json, plan_day FROM config WHERE id = 1`).
		Scan(&stationRaw, &pricesRaw, &day)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &service.ErrNotFound{What: "配置"}
	}
	if err != nil {
		return nil, err
	}
	return decodeConfig(stationRaw, pricesRaw, day)
}

// SaveConfig 覆盖配置并清空该计划日的旧版本与遥测。
func (p *PG) SaveConfig(ctx context.Context, cfg service.Config) error {
	stationRaw, err := json.Marshal(cfg.Station)
	if err != nil {
		return err
	}
	pricesRaw, err := json.Marshal(cfg.Prices)
	if err != nil {
		return err
	}
	day, err := time.Parse("2006-01-02", cfg.PlanDay)
	if err != nil {
		return err
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO config (id, station_json, prices_json, plan_day)
		 VALUES (1, $1, $2, $3)
		 ON CONFLICT (id) DO UPDATE
		 SET station_json = EXCLUDED.station_json,
		     prices_json = EXCLUDED.prices_json,
		     plan_day = EXCLUDED.plan_day,
		     updated_at = now()`,
		string(stationRaw), string(pricesRaw), day); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM plans WHERE plan_day = $1`, day); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM telemetry WHERE plan_day = $1`, day); err != nil {
		return err
	}
	return tx.Commit()
}

// NextVersionNumber 实现 service.Store。
func (p *PG) NextVersionNumber(ctx context.Context, planDay string) (int, error) {
	day, err := time.Parse("2006-01-02", planDay)
	if err != nil {
		return 0, err
	}
	var n int
	if err := p.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM plans WHERE plan_day = $1`, day).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// InsertVersion 写入版本并切换 is_current。
func (p *PG) InsertVersion(ctx context.Context, v *service.PlanVersion) error {
	day, err := time.Parse("2006-01-02", v.PlanDay)
	if err != nil {
		return err
	}
	b, err := json.Marshal(v.ChargeMW)
	if err != nil {
		return err
	}
	bd, err := json.Marshal(v.DischargeMW)
	if err != nil {
		return err
	}
	bs, err := json.Marshal(v.Soc)
	if err != nil {
		return err
	}
	bfc, err := json.Marshal(v.FrozenChargeMW)
	if err != nil {
		return err
	}
	bfd, err := json.Marshal(v.FrozenDischargeMW)
	if err != nil {
		return err
	}
	bfs, err := json.Marshal(v.FrozenSoc)
	if err != nil {
		return err
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE plans SET is_current = FALSE WHERE plan_day = $1 AND is_current = TRUE`, day); err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx,
		`INSERT INTO plans (plan_day, version, is_current, trigger_reason, start_period,
		                    start_energy, charge_mw, discharge_mw, soc,
		                    frozen_charge, frozen_discharge, frozen_soc,
		                    profit_yuan, grid_points, grid_gap_mwh, error_bound_yuan)
		 VALUES ($1,$2,TRUE,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		 RETURNING id, created_at`,
		day, v.Version, v.TriggerReason, v.StartPeriod, v.StartEnergyMWh,
		string(b), string(bd), string(bs),
		string(bfc), string(bfd), string(bfs),
		v.ProfitYuan, v.GridPoints, v.GridGapMWh, v.ErrorBoundYuan).
		Scan(&v.ID, &v.CreatedAt)
	if err != nil {
		return err
	}
	v.IsCurrent = true
	return tx.Commit()
}

// CurrentVersion 实现 service.Store。
func (p *PG) CurrentVersion(ctx context.Context, planDay string) (*service.PlanVersion, error) {
	day, err := time.Parse("2006-01-02", planDay)
	if err != nil {
		return nil, err
	}
	rows, err := queryVersions(ctx, p.db,
		`SELECT `+versionCols+` FROM plans WHERE plan_day = $1 AND is_current = TRUE`, day)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, &service.ErrNotFound{What: "当前计划版本"}
	}
	return rows[0], nil
}

// GetVersion 实现 service.Store。
func (p *PG) GetVersion(ctx context.Context, planDay string, version int) (*service.PlanVersion, error) {
	day, err := time.Parse("2006-01-02", planDay)
	if err != nil {
		return nil, err
	}
	rows, err := queryVersions(ctx, p.db,
		`SELECT `+versionCols+` FROM plans WHERE plan_day = $1 AND version = $2`, day, version)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, &service.ErrNotFound{What: fmt.Sprintf("计划版本 %d", version)}
	}
	return rows[0], nil
}

// ListVersions 实现 service.Store。
func (p *PG) ListVersions(ctx context.Context, planDay string) ([]*service.PlanVersion, int, error) {
	day, err := time.Parse("2006-01-02", planDay)
	if err != nil {
		return nil, 0, err
	}
	rows, err := queryVersions(ctx, p.db,
		`SELECT `+versionCols+` FROM plans WHERE plan_day = $1 ORDER BY version DESC`, day)
	if err != nil {
		return nil, 0, err
	}
	cur := 0
	for _, r := range rows {
		if r.IsCurrent {
			cur = r.Version
		}
	}
	return rows, cur, nil
}

// InsertTelemetry 幂等写入。
func (p *PG) InsertTelemetry(ctx context.Context, t service.Telemetry) (bool, error) {
	day, err := time.Parse("2006-01-02", t.PlanDay)
	if err != nil {
		return false, err
	}
	ct, err := p.db.ExecContext(ctx,
		`INSERT INTO telemetry (id, plan_day, ts, period_index, soc, charge_mw, discharge_mw)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (id) DO NOTHING`,
		t.ID, day, t.Timestamp.UTC(), t.PeriodIndex, t.Soc, t.ChargeMW, t.DischargeMW)
	if err != nil {
		return false, err
	}
	n, err := ct.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ListTelemetry 实现 service.Store。
func (p *PG) ListTelemetry(ctx context.Context, planDay string) ([]service.Telemetry, error) {
	day, err := time.Parse("2006-01-02", planDay)
	if err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, plan_day, ts, period_index, soc, charge_mw, discharge_mw, received_at
		 FROM telemetry WHERE plan_day = $1 ORDER BY ts ASC, id ASC`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []service.Telemetry{}
	for rows.Next() {
		var t service.Telemetry
		var d time.Time
		if err := rows.Scan(&t.ID, &d, &t.Timestamp, &t.PeriodIndex,
			&t.Soc, &t.ChargeMW, &t.DischargeMW, &t.ReceivedAt); err != nil {
			return nil, err
		}
		t.PlanDay = planDay
		out = append(out, t)
	}
	return out, rows.Err()
}

const versionCols = `id, plan_day, version, is_current, trigger_reason, start_period,
		start_energy, charge_mw, discharge_mw, soc,
		frozen_charge, frozen_discharge, frozen_soc,
		profit_yuan, grid_points, grid_gap_mwh, error_bound_yuan, created_at`

type scanner interface {
	Scan(dest ...any) error
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryVersions(ctx context.Context, q queryer, query string, args ...any) ([]*service.PlanVersion, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*service.PlanVersion
	for rows.Next() {
		v := &service.PlanVersion{}
		var day time.Time
		var ch, dch, soc, fch, fdch, fsoc []byte
		if err := rows.Scan(
			&v.ID, &day, &v.Version, &v.IsCurrent, &v.TriggerReason, &v.StartPeriod,
			&v.StartEnergyMWh, &ch, &dch, &soc, &fch, &fdch, &fsoc,
			&v.ProfitYuan, &v.GridPoints, &v.GridGapMWh, &v.ErrorBoundYuan, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.PlanDay = day.Format("2006-01-02")
		if err := json.Unmarshal(ch, &v.ChargeMW); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(dch, &v.DischargeMW); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(soc, &v.Soc); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(fch, &v.FrozenChargeMW); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(fdch, &v.FrozenDischargeMW); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(fsoc, &v.FrozenSoc); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func decodeConfig(stationRaw, pricesRaw []byte, day time.Time) (*service.Config, error) {
	cfg := &service.Config{PlanDay: day.Format("2006-01-02")}
	if err := json.Unmarshal(stationRaw, &cfg.Station); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(pricesRaw, &cfg.Prices); err != nil {
		return nil, err
	}
	return cfg, nil
}
