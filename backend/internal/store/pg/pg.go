// Package pg 提供 Store 的 PostgreSQL 16 实现（pgx/v5 连接池）。
// 所有状态（参数、电价、计划版本、遥测）都入库，服务重启后当前计划版本不变。
package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"essplanner/internal/core"
	"essplanner/internal/store"
)

const schema = `
CREATE TABLE IF NOT EXISTS station_params (
    id                        SMALLINT PRIMARY KEY CHECK (id = 1),
    energy_mwh                DOUBLE PRECISION NOT NULL,
    max_charge_mw             DOUBLE PRECISION NOT NULL,
    max_discharge_mw          DOUBLE PRECISION NOT NULL,
    soc_min                   DOUBLE PRECISION NOT NULL,
    soc_max                   DOUBLE PRECISION NOT NULL,
    charge_eff                DOUBLE PRECISION NOT NULL,
    discharge_eff             DOUBLE PRECISION NOT NULL,
    degradation_cost_per_mwh  DOUBLE PRECISION NOT NULL,
    end_soc_min               DOUBLE PRECISION NOT NULL,
    deviation_threshold_mwh   DOUBLE PRECISION NOT NULL,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS prices (
    date    DATE PRIMARY KEY,
    prices  JSONB NOT NULL
);
CREATE TABLE IF NOT EXISTS plans (
    id               BIGSERIAL PRIMARY KEY,
    date             DATE NOT NULL,
    version          INT NOT NULL,
    trigger_reason   TEXT NOT NULL,
    expected_revenue DOUBLE PRECISION NOT NULL,
    initial_soc_mwh  DOUBLE PRECISION NOT NULL,
    based_on_ts      TIMESTAMPTZ,
    slots            JSONB NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (date, version)
);
CREATE TABLE IF NOT EXISTS telemetry (
    id          TEXT PRIMARY KEY,
    date        DATE NOT NULL,
    ts          TIMESTAMPTZ NOT NULL,
    slot        INT NOT NULL,
    soc_mwh     DOUBLE PRECISION NOT NULL,
    power_mw    DOUBLE PRECISION NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS telemetry_date_ts_idx ON telemetry (date, ts);
`

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("初始化数据库结构失败: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func parseDate(date string) (time.Time, error) {
	return time.Parse("2006-01-02", date)
}

func (s *Store) GetStation(ctx context.Context) (*core.StationParams, error) {
	var p core.StationParams
	err := s.pool.QueryRow(ctx, `
		SELECT energy_mwh, max_charge_mw, max_discharge_mw, soc_min, soc_max,
		       charge_eff, discharge_eff, degradation_cost_per_mwh, end_soc_min, deviation_threshold_mwh
		FROM station_params WHERE id = 1`).Scan(
		&p.EnergyMWh, &p.MaxChargeMW, &p.MaxDischargeMW, &p.SocMin, &p.SocMax,
		&p.ChargeEff, &p.DischargeEff, &p.DegradationCostPerMWh, &p.EndSocMin, &p.DeviationThresholdMWh)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) PutStation(ctx context.Context, p *core.StationParams) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO station_params (id, energy_mwh, max_charge_mw, max_discharge_mw, soc_min, soc_max,
		                            charge_eff, discharge_eff, degradation_cost_per_mwh, end_soc_min,
		                            deviation_threshold_mwh, updated_at)
		VALUES (1, $1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now())
		ON CONFLICT (id) DO UPDATE SET
		    energy_mwh=$1, max_charge_mw=$2, max_discharge_mw=$3, soc_min=$4, soc_max=$5,
		    charge_eff=$6, discharge_eff=$7, degradation_cost_per_mwh=$8, end_soc_min=$9,
		    deviation_threshold_mwh=$10, updated_at=now()`,
		p.EnergyMWh, p.MaxChargeMW, p.MaxDischargeMW, p.SocMin, p.SocMax,
		p.ChargeEff, p.DischargeEff, p.DegradationCostPerMWh, p.EndSocMin, p.DeviationThresholdMWh)
	return err
}

func (s *Store) GetPrices(ctx context.Context, date string) ([]float64, error) {
	d, err := parseDate(date)
	if err != nil {
		return nil, store.ErrNotFound
	}
	var raw []byte
	err = s.pool.QueryRow(ctx, `SELECT prices FROM prices WHERE date = $1`, d).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var prices []float64
	if err := json.Unmarshal(raw, &prices); err != nil {
		return nil, err
	}
	return prices, nil
}

func (s *Store) PutPrices(ctx context.Context, date string, prices []float64) error {
	d, err := parseDate(date)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(prices)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO prices (date, prices) VALUES ($1, $2)
		ON CONFLICT (date) DO UPDATE SET prices = $2`, d, raw)
	return err
}

func (s *Store) CreatePlan(ctx context.Context, plan *core.Plan) (*core.Plan, error) {
	d, err := parseDate(plan.Date)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(plan.Slots)
	if err != nil {
		return nil, err
	}
	// 版本号在单条语句内原子分配；并发冲突时重试。
	for attempt := 0; attempt < 3; attempt++ {
		cp := *plan
		err = s.pool.QueryRow(ctx, `
			INSERT INTO plans (date, version, trigger_reason, expected_revenue, initial_soc_mwh, based_on_ts, slots)
			SELECT $1, COALESCE(MAX(version), 0) + 1, $2, $3, $4, $5, $6
			FROM plans WHERE date = $1
			RETURNING id, version, created_at`,
			d, plan.TriggerReason, plan.ExpectedRevenue, plan.InitialSocMWh, plan.BasedOnTs, raw,
		).Scan(&cp.ID, &cp.Version, &cp.CreatedAt)
		if err == nil {
			cp.Date = plan.Date
			cp.Slots = plan.Slots
			return &cp, nil
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			return nil, err
		}
	}
	return nil, fmt.Errorf("分配计划版本号失败（并发冲突）")
}

const planCols = `id, version, trigger_reason, expected_revenue, initial_soc_mwh, based_on_ts, slots, created_at`

func (s *Store) scanPlan(row pgx.Row, date string) (*core.Plan, error) {
	var p core.Plan
	var raw []byte
	err := row.Scan(&p.ID, &p.Version, &p.TriggerReason, &p.ExpectedRevenue,
		&p.InitialSocMWh, &p.BasedOnTs, &raw, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &p.Slots); err != nil {
		return nil, err
	}
	p.Date = date
	return &p, nil
}

func (s *Store) GetCurrentPlan(ctx context.Context, date string) (*core.Plan, error) {
	d, err := parseDate(date)
	if err != nil {
		return nil, store.ErrNotFound
	}
	row := s.pool.QueryRow(ctx, `
		SELECT `+planCols+` FROM plans WHERE date = $1
		ORDER BY version DESC LIMIT 1`, d)
	return s.scanPlan(row, date)
}

func (s *Store) GetPlanVersion(ctx context.Context, date string, version int) (*core.Plan, error) {
	d, err := parseDate(date)
	if err != nil {
		return nil, store.ErrNotFound
	}
	row := s.pool.QueryRow(ctx, `
		SELECT `+planCols+` FROM plans WHERE date = $1 AND version = $2`, d, version)
	return s.scanPlan(row, date)
}

func (s *Store) ListPlanVersions(ctx context.Context, date string) ([]core.PlanMeta, error) {
	d, err := parseDate(date)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, version, trigger_reason, expected_revenue, initial_soc_mwh, based_on_ts, created_at
		FROM plans WHERE date = $1 ORDER BY version DESC`, d)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	metas := []core.PlanMeta{}
	for rows.Next() {
		var m core.PlanMeta
		if err := rows.Scan(&m.ID, &m.Version, &m.TriggerReason, &m.ExpectedRevenue,
			&m.InitialSocMWh, &m.BasedOnTs, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Date = date
		metas = append(metas, m)
	}
	return metas, rows.Err()
}

func (s *Store) InsertTelemetry(ctx context.Context, t *core.Telemetry) (bool, error) {
	d, err := parseDate(t.Date)
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO telemetry (id, date, ts, slot, soc_mwh, power_mw)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`,
		t.ID, d, t.Ts, t.Slot, t.SocMWh, t.PowerMW)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) LatestTelemetry(ctx context.Context, date string) (*core.Telemetry, error) {
	d, err := parseDate(date)
	if err != nil {
		return nil, store.ErrNotFound
	}
	var tm core.Telemetry
	err = s.pool.QueryRow(ctx, `
		SELECT id, ts, slot, soc_mwh, power_mw FROM telemetry
		WHERE date = $1 ORDER BY ts DESC, received_at DESC LIMIT 1`, d).
		Scan(&tm.ID, &tm.Ts, &tm.Slot, &tm.SocMWh, &tm.PowerMW)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	tm.Date = date
	return &tm, nil
}

func (s *Store) ListTelemetry(ctx context.Context, date string) ([]core.Telemetry, error) {
	d, err := parseDate(date)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, ts, slot, soc_mwh, power_mw FROM telemetry
		WHERE date = $1 ORDER BY ts`, d)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Telemetry{}
	for rows.Next() {
		var tm core.Telemetry
		if err := rows.Scan(&tm.ID, &tm.Ts, &tm.Slot, &tm.SocMWh, &tm.PowerMW); err != nil {
			return nil, err
		}
		tm.Date = date
		out = append(out, tm)
	}
	return out, rows.Err()
}
