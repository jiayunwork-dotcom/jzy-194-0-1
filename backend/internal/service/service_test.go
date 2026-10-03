package service

import (
	"context"
	"math"
	"testing"
	"time"

	"energystorage/internal/spec"
)

const day = "2026-10-04"

func testStation() spec.Station {
	return spec.Station{
		RatedEnergyMWh:        4,
		MaxChargeMW:           2,
		MaxDischargeMW:        2,
		SocMin:                0,
		SocMax:                1,
		ChargeEfficiency:      0.9,
		DischargeEfficiency:   0.9,
		DegradationCostPerMWh: 0,
		SocEndMin:             0,
		SocInitial:            0,
		DeviationThresholdMWh: 0.1,
		GridPoints:            120,
	}
}

// simplePrices 构造有明显峰谷的价格：0-31 谷，32-63 峰，64-95 平。
func simplePrices() []float64 {
	p := make([]float64, spec.Periods)
	for i := range p {
		switch {
		case i < 32:
			p[i] = 50
		case i < 64:
			p[i] = 400
		default:
			p[i] = 200
		}
	}
	return p
}

func newTestService(now time.Time) *Service {
	return New(NewMemoryStore(), nil, func() time.Time { return now })
}

func ts(hour, minute int) time.Time {
	return time.Date(2026, 10, 4, hour, minute, 0, 0, time.FixedZone("CST", 8*3600))
}

func TestConfigureCreatesVersionOne(t *testing.T) {
	svc := newTestService(ts(8, 0))
	_, v, err := svc.Configure(context.Background(), testStation(), simplePrices(), day)
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if v.Version != 1 || !v.IsCurrent || v.StartPeriod != 0 {
		t.Fatalf("首个版本异常: %+v", v)
	}
	if len(v.ChargeMW) != spec.Periods {
		t.Fatalf("应给出 96 点计划，实际 %d", len(v.ChargeMW))
	}
	// 谷段有充电、峰段有放电，说明计划确实在套利。
	var valleyCh, peakDch float64
	for i := 0; i < 32; i++ {
		valleyCh += v.ChargeMW[i]
	}
	for i := 32; i < 64; i++ {
		peakDch += v.DischargeMW[i]
	}
	if valleyCh == 0 || peakDch == 0 {
		t.Fatalf("谷充峰放为空: ch=%v dch=%v", valleyCh, peakDch)
	}
}

func TestTelemetryDedup(t *testing.T) {
	svc := newTestService(ts(0, 30))
	_, _, err := svc.Configure(context.Background(), testStation(), simplePrices(), day)
	if err != nil {
		t.Fatal(err)
	}
	tel := Telemetry{ID: "T1", Timestamp: ts(1, 0), Soc: 0.02, ChargeMW: 1.0, DischargeMW: 0}
	r1, err := svc.IngestTelemetry(context.Background(), tel)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Duplicate {
		t.Fatalf("首次上报不应判重")
	}
	r2, err := svc.IngestTelemetry(context.Background(), tel)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Duplicate {
		t.Fatalf("重复遥测编号应判重")
	}
	tels, _ := svc.ListTelemetry(context.Background(), day)
	if len(tels) != 1 {
		t.Fatalf("重复上报只应存一条，实际 %d", len(tels))
	}
}

func TestTelemetryOutsidePlanDayRejected(t *testing.T) {
	svc := newTestService(ts(8, 0))
	_, _, _ = svc.Configure(context.Background(), testStation(), simplePrices(), day)
	bad := []Telemetry{
		{ID: "X1", Timestamp: ts(23, 59).Add(time.Hour), Soc: 0.1},  // 次日 00:59
		{ID: "X2", Timestamp: ts(0, 0).Add(-time.Minute), Soc: 0.1}, // 前一天 23:59
	}
	for i, b := range bad {
		_, err := svc.IngestTelemetry(context.Background(), b)
		if err == nil {
			t.Fatalf("case %d: 计划日外遥测应拒收", i)
		}
		fe, ok := err.(*spec.FieldError)
		if !ok || !containsStr(fe.Fields, "timestamp") {
			t.Fatalf("case %d: 应指出 timestamp 字段，实际 %v", i, err)
		}
	}
}

func TestTelemetryFieldValidation(t *testing.T) {
	svc := newTestService(ts(8, 0))
	_, _, _ = svc.Configure(context.Background(), testStation(), simplePrices(), day)
	cases := []struct {
		t    Telemetry
		want string
	}{
		{Telemetry{Timestamp: ts(1, 0), Soc: 0.5}, "id"},
		{Telemetry{ID: "A", Timestamp: ts(1, 0), Soc: 1.5}, "soc"},
		{Telemetry{ID: "A", Timestamp: ts(1, 0), Soc: -0.1}, "soc"},
		{Telemetry{ID: "A", Timestamp: ts(1, 0), Soc: 0.5, ChargeMW: -1}, "charge_mw"},
		{Telemetry{ID: "A", Timestamp: ts(1, 0), Soc: 0.5, DischargeMW: -1}, "discharge_mw"},
	}
	for i, c := range cases {
		_, err := svc.IngestTelemetry(context.Background(), c.t)
		if err == nil {
			t.Fatalf("case %d 应拒收", i)
		}
		fe, ok := err.(*spec.FieldError)
		if !ok || !containsStr(fe.Fields, c.want) {
			t.Fatalf("case %d 应指出 %s，实际 %v", i, c.want, err)
		}
	}
}

func TestDeviationTriggersNewVersion(t *testing.T) {
	svc := newTestService(ts(1, 0))
	st := testStation()
	st.SocInitial = 0.25 // 初始 1 MWh
	_, _, err := svc.Configure(context.Background(), st, simplePrices(), day)
	if err != nil {
		t.Fatal(err)
	}
	// 第 4 时段（01:00）实际 SOC 与计划偏差 0.5 MWh（阈值 0.1）。
	res, err := svc.IngestTelemetry(context.Background(), Telemetry{
		ID: "D1", Timestamp: ts(1, 0), Soc: 0.375, // 1.5 MWh
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reoptimized || res.NewVersion == nil {
		t.Fatalf("偏差超阈值应产生新版本，got %+v warning=%v", res.Reoptimized, res.Warning)
	}
	if res.NewVersion.Version != 2 || !res.NewVersion.IsCurrent {
		t.Fatalf("应为当前版本 v2")
	}
	if res.NewVersion.StartPeriod != 5 {
		t.Fatalf("应从第 5 时段开始重算，实际 %d", res.NewVersion.StartPeriod)
	}
	if len(res.NewVersion.FrozenSoc) != 5 {
		t.Fatalf("冻结前缀长度应为 5，实际 %d", len(res.NewVersion.FrozenSoc))
	}

	versions, cur, err := svc.ListVersions(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || cur != 2 {
		t.Fatalf("应保留 2 个版本且当前为 v2")
	}
	old, err := svc.PlanByVersion(context.Background(), day, 1)
	if err != nil {
		t.Fatal(err)
	}
	if old.IsCurrent {
		t.Fatalf("旧版本不应标记为当前")
	}
	if len(old.FullCharge()) != spec.Periods {
		t.Fatalf("旧版本完整视图应为 96 点")
	}
}

func TestSmallDeviationDoesNotReplan(t *testing.T) {
	svc := newTestService(ts(1, 0))
	st := testStation()
	st.SocInitial = 0.25
	_, _, _ = svc.Configure(context.Background(), st, simplePrices(), day)
	cur, _ := svc.CurrentPlan(context.Background(), day)
	planned := cur.FullSoc()[4]
	res, err := svc.IngestTelemetry(context.Background(), Telemetry{
		ID: "S1", Timestamp: ts(1, 0), Soc: planned + 0.01/st.RatedEnergyMWh, // 偏差 0.01 MWh
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reoptimized {
		t.Fatalf("偏差 %.4f MWh 小于阈值，不应重算", res.DeviationMWh)
	}
	if res.DeviationMWh > 0.1+1e-9 {
		t.Fatalf("偏差计算异常 %.4f", res.DeviationMWh)
	}
}

func TestLateTelemetryDoesNotChangePast(t *testing.T) {
	svc := newTestService(ts(5, 0))
	st := testStation()
	st.SocInitial = 0.25
	_, _, _ = svc.Configure(context.Background(), st, simplePrices(), day)
	// 先在第 20 时段触发一次重算。
	_, err := svc.IngestTelemetry(context.Background(), Telemetry{
		ID: "L0", Timestamp: time.Date(2026, 10, 4, 5, 5, 0, 0, time.FixedZone("CST", 8*3600)),
		Soc: 0.9,
	})
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := svc.CurrentPlan(context.Background(), day)
	if cur.StartPeriod != 21 {
		t.Fatalf("前置条件：当前版本应从 21 开始，实际 %d", cur.StartPeriod)
	}
	// 晚到的第 2 时段遥测：归位存档，但不允许改过去时段。
	res, err := svc.IngestTelemetry(context.Background(), Telemetry{
		ID: "L1", Timestamp: ts(0, 30), Soc: 0.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reoptimized {
		t.Fatalf("晚到遥测落在冻结段，不应触发重算")
	}
	tels, _ := svc.ListTelemetry(context.Background(), day)
	if len(tels) != 2 {
		t.Fatalf("晚到遥测应已存档")
	}
	// 时刻升序归位：L1(00:30) 在 L0(05:05) 之前。
	if tels[0].ID != "L1" || tels[1].ID != "L0" {
		t.Fatalf("遥测未按时刻归位: %s %s", tels[0].ID, tels[1].ID)
	}
	cur2, _ := svc.CurrentPlan(context.Background(), day)
	if cur2.Version != cur.Version || cur2.StartPeriod != 21 {
		t.Fatalf("当前版本不应变化")
	}
}

func TestReoptimizedPlanStaysFeasible(t *testing.T) {
	svc := newTestService(ts(1, 0))
	st := testStation()
	st.SocInitial = 0.5
	st.SocEndMin = 0.2
	_, _, err := svc.Configure(context.Background(), st, simplePrices(), day)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.IngestTelemetry(context.Background(), Telemetry{
		ID: "F1", Timestamp: ts(1, 0), Soc: 0.8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reoptimized {
		t.Fatalf("应重算: %v", res.Warning)
	}
	v := res.NewVersion
	fullSoc := v.FullSoc()
	for i, s := range fullSoc {
		if s < st.SocMin-1e-9 || s > st.SocMax+1e-9 {
			t.Fatalf("时段 %d SOC %.4f 越界", i, s)
		}
	}
	if fullSoc[spec.Periods-1] < st.SocEndMin-1e-9 {
		t.Fatalf("日末 SOC %.4f 低于下限", fullSoc[spec.Periods-1])
	}
	// 冻结前缀与冻结边界衔接。
	if got := math.Abs(fullSoc[v.StartPeriod-1] - v.FrozenSoc[len(v.FrozenSoc)-1]); got > 1e-12 {
		t.Fatalf("冻结段末尾 SOC 不一致 %.2e", got)
	}
}

func TestManualReoptimize(t *testing.T) {
	now := ts(2, 0)
	svc := newTestService(now)
	st := testStation()
	st.SocInitial = 0.3
	_, _, _ = svc.Configure(context.Background(), st, simplePrices(), day)
	v, err := svc.ManualReoptimize(context.Background(), day, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != 2 || v.StartPeriod != 9 { // now 02:00 -> period 8，冻结到 8
		t.Fatalf("手工重优化版本异常: v%d start=%d", v.Version, v.StartPeriod)
	}
}

func TestCurrentVersionSurvivesNewStoreInstance(t *testing.T) {
	// “服务重启后当前计划版本不变”：状态持久化在 Store 中，
	// 用新的 Service 包装同一 Store 模拟重启。
	mem := NewMemoryStore()
	svc1 := New(mem, nil, func() time.Time { return ts(1, 0) })
	st := testStation()
	st.SocInitial = 0.25
	_, _, _ = svc1.Configure(context.Background(), st, simplePrices(), day)
	_, _ = svc1.IngestTelemetry(context.Background(), Telemetry{ID: "R1", Timestamp: ts(1, 0), Soc: 0.9})

	svc2 := New(mem, nil, func() time.Time { return ts(1, 0) })
	v, err := svc2.CurrentPlan(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != 2 || !v.IsCurrent {
		t.Fatalf("重启后当前版本应仍是 v2，实际 v%d current=%v", v.Version, v.IsCurrent)
	}
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
