-- 0001_init.sql 储能电站运营系统初始表结构。

-- 当前生效的电站参数与电价（单行配置，id 固定为 1）。
CREATE TABLE IF NOT EXISTS config (
    id           INTEGER PRIMARY KEY DEFAULT 1,
    station_json JSONB       NOT NULL,
    prices_json  JSONB       NOT NULL,
    plan_day     DATE        NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT config_singleton CHECK (id = 1)
);

-- 计划版本。每个计划日可有多个版本（日前计划 + 滚动修正），旧版本全部保留。
CREATE TABLE IF NOT EXISTS plans (
    id              BIGSERIAL PRIMARY KEY,
    plan_day        DATE        NOT NULL,
    version         INTEGER     NOT NULL,
    is_current      BOOLEAN     NOT NULL DEFAULT FALSE,
    trigger_reason  TEXT        NOT NULL,
    start_period    INTEGER     NOT NULL,
    start_energy    DOUBLE PRECISION NOT NULL,
    charge_mw       JSONB       NOT NULL,
    discharge_mw    JSONB       NOT NULL,
    soc             JSONB       NOT NULL,
    frozen_charge   JSONB       NOT NULL DEFAULT '[]',
    frozen_discharge JSONB      NOT NULL DEFAULT '[]',
    frozen_soc      JSONB       NOT NULL DEFAULT '[]',
    profit_yuan     DOUBLE PRECISION NOT NULL,
    grid_points     INTEGER     NOT NULL,
    grid_gap_mwh    DOUBLE PRECISION NOT NULL,
    error_bound_yuan DOUBLE PRECISION NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (plan_day, version)
);

-- 遥测：id 为上报方提供的遥测编号，重复上报只算一次。
CREATE TABLE IF NOT EXISTS telemetry (
    id                TEXT PRIMARY KEY,
    plan_day          DATE        NOT NULL,
    ts                TIMESTAMPTZ NOT NULL,
    period_index      INTEGER     NOT NULL,
    soc               DOUBLE PRECISION NOT NULL,
    charge_mw         DOUBLE PRECISION NOT NULL,
    discharge_mw      DOUBLE PRECISION NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_plans_day_version ON plans (plan_day, version DESC);
CREATE INDEX IF NOT EXISTS idx_telemetry_day_ts ON telemetry (plan_day, ts);
