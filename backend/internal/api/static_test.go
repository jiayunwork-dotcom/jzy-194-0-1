package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"energystorage/internal/service"
)

func TestStaticServingAndSPAFallback(t *testing.T) {
	memFS := fstest.MapFS{
		"dist/index.html":    {Data: []byte("<!doctype html><title>bess</title>"), Mode: 0o644},
		"dist/assets/app.js": {Data: []byte("console.log(1)"), Mode: 0o644},
	}
	svc := service.New(service.NewMemoryStore(), nil, func() time.Time { return time.Now() })
	r := NewServer(svc, memFS)

	// 存在的资源直接返回。
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "console.log(1)" {
		t.Fatalf("静态资源异常: %d %q", w.Code, w.Body.String())
	}

	// 未知前端路由回退 index.html（SPA），但 /api 路径不回退。
	req = httptest.NewRequest(http.MethodGet, "/some/spa/route", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() == "" {
		t.Fatalf("SPA 回退异常: %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/nonexistent", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("未知 API 路径不应回退到 index.html")
	}
}
