package pg_test

import (
	"context"
	"os"
	"testing"
	"time"

	"essplanner/internal/core"
	"essplanner/internal/store/pg"
)

// PostgreSQL 集成测试：需要 TEST_DATABASE_URL（如
// postgres://energy:energy@localhost:5432/energy?sslmode=disable），否则跳过。
// 可用 docker compose 起库后运行：
//
//	docker compose up -d db
//	TEST_DATABASE_URL=postgres://energy:energy@localhost:5432/energy?sslmode=disable go test ./internal/store/pg/
func dsn(t *testing.T) string {
	t.Helper()
	d := os.Getenv("TEST_DATABASE_URL")
	if d == "" {
		t.Skip("TEST_DATABASE_URL 未设置，跳过 PostgreSQL 集成测试")
	}
	return d
}

func TestPgStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := pg.New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	date := "1999-12-31" // 专用测试日期，避免污染
	params := &core.StationParams{
		EnergyMWh: 10, MaxChargeMW: 5, MaxDischargeMW: 5,
		SocMin: 0.1, SocMax: 0.9, ChargeEff: 0.9, DischargeEff: 0.9,
		DegradationCostPerMWh: 2, EndSocMin: 0.2, DeviationThresholdMWh: 0.5,
	}
	if err := st.PutStation(ctx, params); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetStation(ctx)
	if err != nil || *got != *params {
		t.Fatalf("电站参数往返不一致: %+v vs %+v (%v)", got, params, err)
	}

	prices := make([]float64, 96)
	for i := range prices {
		prices[i] = float64(i)
	}
	if err := st.PutPrices(ctx, date, prices); err != nil {
		t.Fatal(err)
	}
	gotPrices, err := st.GetPrices(ctx, date)
	if err != nil || len(gotPrices) != 96 || gotPrices[95] != 95 {
		t.Fatalf("电价往返不一致: %v", err)
	}

	// 版本号原子递增
	p1, err := st.CreatePlan(ctx, &core.Plan{Date: date, TriggerReason: "t1", Slots: []core.SlotPlan{{Slot: 0, PowerMW: 1, SocMWh: 2}}})
	if err != nil || p1.Version != 1 {
		t.Fatalf("版本1创建失败: %v %v", p1, err)
	}
	p2, err := st.CreatePlan(ctx, &core.Plan{Date: date, TriggerReason: "t2", Slots: []core.SlotPlan{}})
	if err != nil || p2.Version != 2 {
		t.Fatalf("版本2创建失败: %v %v", p2, err)
	}
	cur, err := st.GetCurrentPlan(ctx, date)
	if err != nil || cur.Version != 2 {
		t.Fatalf("当前版本应为 2: %v %v", cur, err)
	}
	v1, err := st.GetPlanVersion(ctx, date, 1)
	if err != nil || len(v1.Slots) != 1 || v1.Slots[0].PowerMW != 1 {
		t.Fatalf("历史版本读取失败: %v %v", v1, err)
	}
	metas, err := st.ListPlanVersions(ctx, date)
	if err != nil || len(metas) != 2 || metas[0].Version != 2 {
		t.Fatalf("版本列表异常: %+v %v", metas, err)
	}

	// 遥测幂等 + 按时刻归位
	ts1 := time.Date(1999, 12, 31, 10, 0, 0, 0, time.UTC)
	ok, err := st.InsertTelemetry(ctx, &core.Telemetry{ID: "it-1", Date: date, Ts: ts1, Slot: 40, SocMWh: 5, PowerMW: 1})
	if err != nil || !ok {
		t.Fatalf("首次写入遥测失败: %v %v", ok, err)
	}
	ok, err = st.InsertTelemetry(ctx, &core.Telemetry{ID: "it-1", Date: date, Ts: ts1, Slot: 40, SocMWh: 9, PowerMW: 9})
	if err != nil || ok {
		t.Fatalf("重复遥测应被忽略: %v %v", ok, err)
	}
	ts0 := ts1.Add(-2 * time.Hour)
	if _, err := st.InsertTelemetry(ctx, &core.Telemetry{ID: "it-0", Date: date, Ts: ts0, Slot: 32, SocMWh: 4, PowerMW: 0}); err != nil {
		t.Fatal(err)
	}
	latest, err := st.LatestTelemetry(ctx, date)
	if err != nil || latest.ID != "it-1" {
		t.Fatalf("最新遥测应为 it-1: %+v %v", latest, err)
	}
	list, err := st.ListTelemetry(ctx, date)
	if err != nil || len(list) != 2 || list[0].ID != "it-0" {
		t.Fatalf("遥测未按时刻归位: %+v %v", list, err)
	}
}
