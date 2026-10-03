//go:build pgint

// 存储层集成测试：需要真实 PostgreSQL 16。
// 运行：DATABASE_URL=postgres://... go test -tags=pgint ./internal/store/
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"energystorage/internal/service"
	"energystorage/internal/spec"
)

func TestPGRoundTrip(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("未设置 DATABASE_URL，跳过 PostgreSQL 集成测试")
	}
	ctx := context.Background()
	pg, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pg.Close()

	day := "2026-10-04"
	prices := make([]float64, spec.Periods)
	for i := range prices {
		prices[i] = float64(100 + i)
	}
	cfg := service.Config{PlanDay: day, Station: stationForPG(), Prices: prices}
	if err := pg.SaveConfig(ctx, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := pg.LoadConfig(ctx)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.PlanDay != day || len(got.Prices) != spec.Periods || got.Station.RatedEnergyMWh != 4 {
		t.Fatalf("配置回读异常: %+v", got)
	}

	nines := func(v float64) []float64 {
		a := make([]float64, spec.Periods)
		for i := range a {
			a[i] = v
		}
		return a
	}
	v := &service.PlanVersion{
		PlanDay: day, Version: 1, IsCurrent: true, TriggerReason: "日前计划",
		StartPeriod: 0, StartEnergyMWh: 0.4,
		ChargeMW:    nines(0),
		DischargeMW: nines(0),
		Soc:         nines(0.1),
		ProfitYuan:  123.4, GridPoints: 800, GridGapMWh: 0.005, ErrorBoundYuan: 60,
	}
	v.ChargeMW[0] = 1.5
	if err := pg.InsertVersion(ctx, v); err != nil {
		t.Fatalf("InsertVersion: %v", err)
	}
	cur, err := pg.CurrentVersion(ctx, day)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if cur.Version != 1 || len(cur.FullCharge()) != spec.Periods || cur.ChargeMW[0] != 1.5 {
		t.Fatalf("版本回读异常: %+v", cur)
	}

	// 再插一个版本：is_current 应切换。
	v2 := &service.PlanVersion{
		PlanDay: day, Version: 2, TriggerReason: "遥测触发", StartPeriod: 10,
		StartEnergyMWh: 0.6,
		ChargeMW:       nines(0)[:spec.Periods-10], DischargeMW: nines(0)[:spec.Periods-10],
		Soc:            nines(0.2)[:spec.Periods-10],
		FrozenChargeMW: nines(0.1)[:10], FrozenDischargeMW: nines(0)[:10], FrozenSoc: nines(0.1)[:10],
		ProfitYuan: 100, GridPoints: 800, GridGapMWh: 0.005, ErrorBoundYuan: 55,
	}
	if err := pg.InsertVersion(ctx, v2); err != nil {
		t.Fatalf("InsertVersion v2: %v", err)
	}
	cur2, _ := pg.CurrentVersion(ctx, day)
	if cur2.Version != 2 {
		t.Fatalf("当前版本应为 2，实际 %d", cur2.Version)
	}
	old, err := pg.GetVersion(ctx, day, 1)
	if err != nil || old.IsCurrent {
		t.Fatalf("旧版本应保留且不再 current: %+v err=%v", old, err)
	}
	versions, current, err := pg.ListVersions(ctx, day)
	if err != nil || len(versions) != 2 || current != 2 {
		t.Fatalf("版本列表异常: %v %d %v", versions, current, err)
	}

	tel := service.Telemetry{
		ID: "PG-1", PlanDay: day,
		Timestamp:   time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC),
		PeriodIndex: 4, Soc: 0.3, ChargeMW: 1, DischargeMW: 0,
	}
	inserted, err := pg.InsertTelemetry(ctx, tel)
	if err != nil || !inserted {
		t.Fatalf("InsertTelemetry: inserted=%v err=%v", inserted, err)
	}
	inserted, err = pg.InsertTelemetry(ctx, tel)
	if err != nil || inserted {
		t.Fatalf("重复遥测应幂等: inserted=%v err=%v", inserted, err)
	}
	list, err := pg.ListTelemetry(ctx, day)
	if err != nil || len(list) != 1 || list[0].PeriodIndex != 4 {
		t.Fatalf("遥测列表异常: %d %v", len(list), err)
	}
}

func stationForPG() spec.Station {
	return spec.Station{
		RatedEnergyMWh: 4, MaxChargeMW: 2, MaxDischargeMW: 2,
		SocMin: 0.05, SocMax: 0.95,
		ChargeEfficiency: 0.92, DischargeEfficiency: 0.92,
		DegradationCostPerMWh: 5, SocEndMin: 0.1, SocInitial: 0.1,
		DeviationThresholdMWh: 0.1, GridPoints: 800,
	}
}
