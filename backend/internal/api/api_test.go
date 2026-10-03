package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"essplanner/internal/api"
	"essplanner/internal/service"
	"essplanner/internal/store/memory"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := service.New(memory.New(), time.FixedZone("CST", 8*3600), 500)
	r := api.NewRouter(svc, "")
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func validStationJSON() map[string]interface{} {
	return map[string]interface{}{
		"energy_mwh": 10, "max_charge_mw": 5, "max_discharge_mw": 5,
		"soc_min": 0.1, "soc_max": 0.9,
		"charge_eff": 0.9, "discharge_eff": 0.9,
		"degradation_cost_per_mwh": 2, "end_soc_min": 0.2,
		"deviation_threshold_mwh": 0.5,
	}
}

func pricesJSON() map[string]interface{} {
	// 阶梯递增电价：最优计划会在低价的前 1/4 天充满，便于测试偏差触发
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
	return map[string]interface{}{"prices": prices}
}

func TestStationValidationErrors(t *testing.T) {
	srv := newServer(t)
	bad := validStationJSON()
	bad["charge_eff"] = 1.5
	code, body := do(t, "PUT", srv.URL+"/api/station", bad)
	if code != 400 {
		t.Fatalf("非法效率应 400, 得到 %d", code)
	}
	b, _ := json.Marshal(body)
	if !strings.Contains(string(b), "charge_eff") {
		t.Fatalf("错误应指出 charge_eff 字段: %s", b)
	}
}

func TestPricesLengthValidation(t *testing.T) {
	srv := newServer(t)
	code, body := do(t, "PUT", srv.URL+"/api/prices/2026-10-04", map[string]interface{}{
		"prices": make([]float64, 95),
	})
	if code != 400 {
		t.Fatalf("95 个时段应 400, 得到 %d", code)
	}
	b, _ := json.Marshal(body)
	if !strings.Contains(string(b), "prices") {
		t.Fatalf("错误应指出 prices 字段: %s", b)
	}
}

func TestFullFlowOverHTTP(t *testing.T) {
	srv := newServer(t)
	if code, _ := do(t, "PUT", srv.URL+"/api/station", validStationJSON()); code != 200 {
		t.Fatalf("保存电站参数失败: %d", code)
	}
	if code, _ := do(t, "PUT", srv.URL+"/api/prices/2026-10-04", pricesJSON()); code != 200 {
		t.Fatalf("保存电价失败: %d", code)
	}
	code, plan := do(t, "POST", srv.URL+"/api/plans/2026-10-04/optimize", map[string]interface{}{"initial_soc": 0.2})
	if code != 200 {
		b, _ := json.Marshal(plan)
		t.Fatalf("优化失败 %d: %s", code, b)
	}
	if plan["version"].(float64) != 1 {
		t.Fatalf("首个版本应为 1: %v", plan["version"])
	}
	if len(plan["slots"].([]interface{})) != 96 {
		t.Fatal("计划应含 96 个时段")
	}

	// 遥测：错误日期 → 400 且指出 ts
	code, body := do(t, "POST", srv.URL+"/api/plans/2026-10-04/telemetry", map[string]interface{}{
		"id": "t1", "ts": "2026-10-05T01:00:00+08:00", "soc_mwh": 2, "power_mw": 0,
	})
	if code != 400 {
		t.Fatalf("错误日期遥测应 400, 得到 %d", code)
	}
	b, _ := json.Marshal(body)
	if !strings.Contains(string(b), "ts") {
		t.Fatalf("应指出 ts 字段: %s", b)
	}

	// 正常遥测触发重排
	code, body = do(t, "POST", srv.URL+"/api/plans/2026-10-04/telemetry", map[string]interface{}{
		"id": "t1", "ts": "2026-10-04T05:07:00+08:00", "soc_mwh": 2.0, "power_mw": 0,
	})
	if code != 200 || body["replanned"] != true {
		b, _ := json.Marshal(body)
		t.Fatalf("应触发重排: %d %s", code, b)
	}
	// 重复上报
	code, body = do(t, "POST", srv.URL+"/api/plans/2026-10-04/telemetry", map[string]interface{}{
		"id": "t1", "ts": "2026-10-04T05:07:00+08:00", "soc_mwh": 2.0, "power_mw": 0,
	})
	if code != 200 || body["duplicate"] != true {
		t.Fatalf("重复上报应判重: %v", body)
	}

	// 版本列表
	code, body = do(t, "GET", srv.URL+"/api/plans/2026-10-04/versions", nil)
	if code != 200 {
		t.Fatalf("版本列表失败: %d", code)
	}
	// 当前计划应为 v2
	code, cur := do(t, "GET", srv.URL+"/api/plans/2026-10-04", nil)
	if code != 200 || cur["version"].(float64) != 2 {
		t.Fatalf("当前版本应为 2: %v", cur["version"])
	}
}

func TestNotFound(t *testing.T) {
	srv := newServer(t)
	if code, _ := do(t, "GET", srv.URL+"/api/station", nil); code != 404 {
		t.Fatalf("未录入参数应 404")
	}
	if code, _ := do(t, "GET", srv.URL+"/api/plans/2026-10-04", nil); code != 404 {
		t.Fatalf("无计划应 404")
	}
}
