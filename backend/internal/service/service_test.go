package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"essplanner/internal/core"
	"essplanner/internal/service"
	"essplanner/internal/store/memory"
)

func testStation() *core.StationParams {
	return &core.StationParams{
		EnergyMWh: 10, MaxChargeMW: 5, MaxDischargeMW: 5,
		SocMin: 0.1, SocMax: 0.9,
		ChargeEff: 0.9, DischargeEff: 0.9,
		DegradationCostPerMWh: 2, EndSocMin: 0.2,
		DeviationThresholdMWh: 0.5,
	}
}

// 明显有套利空间的电价：凌晨低、傍晚高。
func testPrices() []float64 {
	prices := make([]float64, 96)
	for i := range prices {
		switch {
		case i < 24:
			prices[i] = 100
		case i < 60:
			prices[i] = 300
		case i < 72:
			prices[i] = 500
		default:
			prices[i] = 800
		}
	}
	return prices
}

func newService(t *testing.T) (*service.Service, context.Context) {
	t.Helper()
	loc := time.FixedZone("CST", 8*3600)
	svc := service.New(memory.New(), loc, 500)
	ctx := context.Background()
	if err := svc.PutStation(ctx, testStation()); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutPrices(ctx, "2026-10-04", testPrices()); err != nil {
		t.Fatal(err)
	}
	return svc, ctx
}

func TestDayAheadCreatesVersions(t *testing.T) {
	svc, ctx := newService(t)
	p1, err := svc.OptimizeDayAhead(ctx, "2026-10-04", 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Version != 1 || len(p1.Slots) != 96 {
		t.Fatalf("版本/时段数异常: v%d, %d slots", p1.Version, len(p1.Slots))
	}
	if p1.ExpectedRevenue <= 0 {
		t.Fatalf("该电价下应有正收益, 得到 %v", p1.ExpectedRevenue)
	}
	p2, err := svc.OptimizeDayAhead(ctx, "2026-10-04", 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Version != 2 {
		t.Fatalf("重复优化应产生版本 2, 得到 v%d", p2.Version)
	}
	cur, err := svc.GetCurrentPlan(ctx, "2026-10-04")
	if err != nil {
		t.Fatal(err)
	}
	if cur.Version != 2 {
		t.Fatalf("当前版本应为 2, 得到 %d", cur.Version)
	}
	metas, _ := svc.ListPlanVersions(ctx, "2026-10-04")
	if len(metas) != 2 || metas[0].Version != 2 {
		t.Fatalf("版本列表异常: %+v", metas)
	}
}

func TestOptimizeValidation(t *testing.T) {
	svc, ctx := newService(t)
	if _, err := svc.OptimizeDayAhead(ctx, "2026-10-04", 0.05); err == nil {
		t.Fatal("起始 SoC 低于下限应报错")
	} else if verrs, ok := err.(core.ValidationError); !ok || verrs[0].Field != "initial_soc" {
		t.Fatalf("应返回 initial_soc 字段错误, 得到 %v", err)
	}
	if _, err := svc.OptimizeDayAhead(ctx, "2026-10-05", 0.2); err == nil {
		t.Fatal("无电价的日期应报错")
	}
	if _, err := svc.OptimizeDayAhead(ctx, "2026-13-40", 0.2); err == nil {
		t.Fatal("非法日期应报错")
	}
}

func TestTelemetryDedup(t *testing.T) {
	svc, ctx := newService(t)
	if _, err := svc.OptimizeDayAhead(ctx, "2026-10-04", 0.2); err != nil {
		t.Fatal(err)
	}
	in := service.TelemetryInput{ID: "tm-1", Ts: "2026-10-04T01:07:00+08:00", SocMWh: 2.0, PowerMW: -5}
	r1, err := svc.SubmitTelemetry(ctx, "2026-10-04", in)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Duplicate {
		t.Fatal("首次上报不应判重")
	}
	r2, err := svc.SubmitTelemetry(ctx, "2026-10-04", in)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Duplicate {
		t.Fatal("重复上报应判重")
	}
	list, _ := svc.ListTelemetry(ctx, "2026-10-04")
	if len(list) != 1 {
		t.Fatalf("重复上报只应记录一次, 实际 %d 条", len(list))
	}
}

func TestTelemetryTriggersReplan(t *testing.T) {
	svc, ctx := newService(t)
	plan, err := svc.OptimizeDayAhead(ctx, "2026-10-04", 0.2)
	if err != nil {
		t.Fatal(err)
	}
	// 时段 20（05:00）计划 SoC 与 2.0 偏差远超阈值 0.5
	r, err := svc.SubmitTelemetry(ctx, "2026-10-04", service.TelemetryInput{
		ID: "tm-1", Ts: "2026-10-04T05:07:00+08:00", SocMWh: 2.0, PowerMW: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Replanned || r.Version != 2 {
		t.Fatalf("应触发重排产生 v2, 得到 %+v", r)
	}
	v2, err := svc.GetCurrentPlan(ctx, "2026-10-04")
	if err != nil {
		t.Fatal(err)
	}
	if v2.BasedOnTs == nil {
		t.Fatal("重排版本应记录依据的遥测时刻")
	}
	if !strings.Contains(v2.TriggerReason, "tm-1") {
		t.Fatalf("触发原因应包含遥测编号: %s", v2.TriggerReason)
	}
	// 已过去的时段不许改
	for i := 0; i <= 20; i++ {
		if v2.Slots[i] != plan.Slots[i] {
			t.Fatalf("时段 %d 被改动: %+v -> %+v", i, plan.Slots[i], v2.Slots[i])
		}
	}
	// 重排起点（时段 21）的 SoC 应从实际值出发
	if v2.Slots[21].SocMWh < 2.0-5*0.25-1e-9 {
		t.Fatalf("重排未从实际 SoC 出发: %+v", v2.Slots[21])
	}
}

func TestTelemetryBelowThresholdNoReplan(t *testing.T) {
	svc, ctx := newService(t)
	if _, err := svc.OptimizeDayAhead(ctx, "2026-10-04", 0.2); err != nil {
		t.Fatal(err)
	}
	r, err := svc.SubmitTelemetry(ctx, "2026-10-04", service.TelemetryInput{
		ID: "tm-1", Ts: "2026-10-04T00:02:00+08:00", SocMWh: 2.1, PowerMW: 0, // 计划起始 2.0，偏差 0.1 < 0.5
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Replanned {
		t.Fatalf("偏差未超阈值不应重排: %+v", r)
	}
}

func TestLateTelemetryDoesNotRetrigger(t *testing.T) {
	svc, ctx := newService(t)
	if _, err := svc.OptimizeDayAhead(ctx, "2026-10-04", 0.2); err != nil {
		t.Fatal(err)
	}
	// 05:07 的遥测触发 v2
	if _, err := svc.SubmitTelemetry(ctx, "2026-10-04", service.TelemetryInput{
		ID: "tm-1", Ts: "2026-10-04T05:07:00+08:00", SocMWh: 2.0, PowerMW: 0,
	}); err != nil {
		t.Fatal(err)
	}
	// 晚到的更早时刻遥测：归位但不触发
	r, err := svc.SubmitTelemetry(ctx, "2026-10-04", service.TelemetryInput{
		ID: "tm-0", Ts: "2026-10-04T03:00:00+08:00", SocMWh: 9.0, PowerMW: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Replanned {
		t.Fatalf("晚到的旧遥测不应触发重排: %+v", r)
	}
	cur, _ := svc.GetCurrentPlan(ctx, "2026-10-04")
	if cur.Version != 2 {
		t.Fatalf("版本不应前进, 当前 v%d", cur.Version)
	}
	// 但列表中按时刻归位
	list, _ := svc.ListTelemetry(ctx, "2026-10-04")
	if len(list) != 2 || list[0].ID != "tm-0" || list[1].ID != "tm-1" {
		t.Fatalf("遥测未按时刻归位: %+v", list)
	}
}

func TestTelemetryWrongDateRejected(t *testing.T) {
	svc, ctx := newService(t)
	if _, err := svc.OptimizeDayAhead(ctx, "2026-10-04", 0.2); err != nil {
		t.Fatal(err)
	}
	_, err := svc.SubmitTelemetry(ctx, "2026-10-04", service.TelemetryInput{
		ID: "tm-x", Ts: "2026-10-05T00:30:00+08:00", SocMWh: 2.0, PowerMW: 0,
	})
	if err == nil {
		t.Fatal("不属于计划日的遥测应拒收")
	}
	verrs, ok := err.(core.ValidationError)
	if !ok || verrs[0].Field != "ts" {
		t.Fatalf("应指出 ts 字段, 得到 %v", err)
	}
}

func TestTelemetryFieldValidation(t *testing.T) {
	svc, ctx := newService(t)
	if _, err := svc.OptimizeDayAhead(ctx, "2026-10-04", 0.2); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		in    service.TelemetryInput
		field string
	}{
		{"空编号", service.TelemetryInput{ID: "", Ts: "2026-10-04T01:00:00+08:00", SocMWh: 1}, "id"},
		{"坏时刻", service.TelemetryInput{ID: "a", Ts: "not-a-time", SocMWh: 1}, "ts"},
		{"SoC越界", service.TelemetryInput{ID: "a", Ts: "2026-10-04T01:00:00+08:00", SocMWh: 11}, "soc_mwh"},
		{"SoC为负", service.TelemetryInput{ID: "a", Ts: "2026-10-04T01:00:00+08:00", SocMWh: -1}, "soc_mwh"},
	}
	for _, c := range cases {
		_, err := svc.SubmitTelemetry(ctx, "2026-10-04", c.in)
		verrs, ok := err.(core.ValidationError)
		if !ok {
			t.Fatalf("%s: 期望 ValidationError, 得到 %v", c.name, err)
		}
		found := false
		for _, fe := range verrs {
			if fe.Field == c.field {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: 应指出字段 %s, 得到 %v", c.name, c.field, verrs)
		}
	}
}
