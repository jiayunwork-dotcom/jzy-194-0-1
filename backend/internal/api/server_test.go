package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"energystorage/internal/service"
	"energystorage/internal/spec"
)

func setupRouter() http.Handler {
	svc := service.New(service.NewMemoryStore(), nil, func() time.Time {
		return time.Date(2026, 10, 4, 2, 0, 0, 0, time.FixedZone("CST", 8*3600))
	})
	return NewServer(svc, nil)
}

func TestConfigLifecycle(t *testing.T) {
	r := setupRouter()

	// 未配置时取配置 404。
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("空配置应 404，实际 %d", w.Code)
	}

	// 非法参数：400 且带字段名。
	bad := `{"station":{"rated_energy_mwh":-1,"max_charge_mw":1,"max_discharge_mw":1,
	"soc_min":0,"soc_max":1,"charge_efficiency":0.9,"discharge_efficiency":0.9,
	"degradation_cost_per_mwh":0,"soc_end_min":0,"soc_initial":0,
	"deviation_threshold_mwh":0.1},"prices":[1],"plan_day":"2026-10-04"}`
	req = httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(bad))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法配置应 400，实际 %d: %s", w.Code, w.Body.String())
	}
	var fe spec.FieldError
	if err := json.Unmarshal(w.Body.Bytes(), &fe); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range fe.Fields {
		if f == "rated_energy_mwh" || f == "prices" {
			found = true
		}
	}
	if !found {
		t.Fatalf("错误响应未指出字段: %v", fe.Fields)
	}

	// 合法配置：200，返回计划。
	prices := make([]float64, spec.Periods)
	for i := range prices {
		if i >= 32 && i < 64 {
			prices[i] = 400
		} else {
			prices[i] = 50
		}
	}
	body, _ := json.Marshal(map[string]any{
		"station": map[string]any{
			"rated_energy_mwh": 4, "max_charge_mw": 2, "max_discharge_mw": 2,
			"soc_min": 0, "soc_max": 1, "charge_efficiency": 0.9, "discharge_efficiency": 0.9,
			"degradation_cost_per_mwh": 0, "soc_end_min": 0, "soc_initial": 0.2,
			"deviation_threshold_mwh": 0.1, "grid_points": 60,
		},
		"prices":   prices,
		"plan_day": "2026-10-04",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("合法配置应 200，实际 %d: %s", w.Code, w.Body.String())
	}
	var cfgResp struct {
		Plan map[string]any `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cfgResp); err != nil {
		t.Fatal(err)
	}
	if v, _ := cfgResp.Plan["version"].(float64); v != 1 {
		t.Fatalf("应返回 v1，实际 %v", cfgResp.Plan["version"])
	}

	// 遥测触发滚动。
	tel, _ := json.Marshal(map[string]any{
		"id": "T1", "timestamp": "2026-10-04T01:00:00+08:00",
		"soc": 0.9, "charge_mw": 0, "discharge_mw": 0,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/telemetry", bytes.NewReader(tel))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusAccepted {
		t.Fatalf("遥测应 200/202，实际 %d: %s", w.Code, w.Body.String())
	}
	var ing map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &ing)
	if nv, _ := ing["new_version"].(map[string]any); nv == nil {
		t.Fatalf("偏差超阈值应返回新版本: %s", w.Body.String())
	}

	// 计划日外遥测 400。
	tel2, _ := json.Marshal(map[string]any{
		"id": "T2", "timestamp": "2026-10-05T01:00:00+08:00", "soc": 0.5,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/telemetry", bytes.NewReader(tel2))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("计划日外遥测应 400，实际 %d", w.Code)
	}

	// 版本列表两条，当前 v2。
	req = httptest.NewRequest(http.MethodGet, "/api/plans?day=2026-10-04", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("版本列表 %d", w.Code)
	}
	var list struct {
		Versions       []map[string]any `json:"versions"`
		CurrentVersion float64          `json:"current_version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Versions) != 2 || list.CurrentVersion != 2 {
		t.Fatalf("应有 2 个版本当前 v2，实际 %d 个当前 v%v", len(list.Versions), list.CurrentVersion)
	}

	// 查历史版本 v1 仍在。
	req = httptest.NewRequest(http.MethodGet, "/api/plans/1?day=2026-10-04", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("历史版本应可取，实际 %d", w.Code)
	}
}

func TestMissingDayParam(t *testing.T) {
	r := setupRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/plans", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺 day 应 400，实际 %d", w.Code)
	}
}
