package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"energystorage/internal/api"
	"energystorage/internal/service"
	"energystorage/internal/spec"
	"energystorage/web"
)

// TestEndToEndWithEmbeddedFrontend 用真实嵌入的前端产物 + 内存存储跑一遍完整流程，
// 覆盖：参数录入 → 日前计划 → 遥测偏差触发新版本 → 版本列表 → 静态首页。
func TestEndToEndWithEmbeddedFrontend(t *testing.T) {
	svc := service.New(service.NewMemoryStore(), nil, func() time.Time {
		return time.Date(2026, 10, 4, 2, 0, 0, 0, time.FixedZone("CST", 8*3600))
	})
	r := api.NewServer(svc, web.Dist)

	do := func(method, path string, body any) (int, map[string]any) {
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var out map[string]any
		if w.Body.Len() > 0 {
			_ = json.Unmarshal(w.Body.Bytes(), &out)
		}
		return w.Code, out
	}

	prices := make([]float64, spec.Periods)
	for i := range prices {
		switch {
		case i < 32:
			prices[i] = 350
		case i < 64:
			prices[i] = 350
		default:
			prices[i] = 350
		}
	}
	// 构造明显谷峰价差。
	for i := 0; i < 32; i++ {
		prices[i] = 50
	}
	code, resp := do(http.MethodPost, "/api/config", map[string]any{
		"station": map[string]any{
			"rated_energy_mwh": 4, "max_charge_mw": 2, "max_discharge_mw": 2,
			"soc_min": 0.05, "soc_max": 0.95, "charge_efficiency": 0.92,
			"discharge_efficiency": 0.92, "degradation_cost_per_mwh": 3,
			"soc_end_min": 0.1, "soc_initial": 0.1,
			"deviation_threshold_mwh": 0.1, "grid_points": 200,
		},
		"prices": prices, "plan_day": "2026-10-04",
	})
	if code != http.StatusOK {
		t.Fatalf("配置失败 %d: %v", code, resp)
	}

	code, resp = do(http.MethodPost, "/api/telemetry", map[string]any{
		"id": "E2E-1", "timestamp": "2026-10-04T03:00:00+08:00",
		"soc": 0.9, "charge_mw": 0, "discharge_mw": 0,
	})
	if code != http.StatusOK && code != http.StatusAccepted {
		t.Fatalf("遥测失败 %d: %v", code, resp)
	}
	if resp["reoptimized"] != true {
		t.Fatalf("大偏差应触发重优化: %v", resp)
	}

	code, resp = do(http.MethodGet, "/api/plans?day=2026-10-04", nil)
	if code != http.StatusOK || resp["current_version"].(float64) != 2 {
		t.Fatalf("版本列表异常 %d: %v", code, resp)
	}

	code, _ = do(http.MethodGet, "/api/plans/1?day=2026-10-04", nil)
	if code != http.StatusOK {
		t.Fatalf("历史版本应可取: %d", code)
	}

	// 嵌入前端：首页与 SPA 回退。
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if code := w.Code; code != http.StatusOK || !strings.Contains(w.Body.String(), "<div id=\"app\">") {
		t.Fatalf("首页异常: %d", code)
	}
	req = httptest.NewRequest(http.MethodGet, "/versions/2", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "app") {
		t.Fatalf("SPA 回退异常: %d", w.Code)
	}
}
