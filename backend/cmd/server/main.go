// 储能电站充放计划系统后端。
//
// 环境变量：
//
//	PORT          监听端口（默认 8080）
//	DATABASE_URL  PostgreSQL 连接串（STORE=pg 时必填）
//	STORE         pg | memory（默认 pg；memory 仅用于本地演示，重启丢数据）
//	STATIC_DIR    前端静态文件目录（默认 ./static）
//	PLAN_TZ       计划日时区（默认 Asia/Shanghai）
//	GRID_POINTS   SoC 网格段数（默认 10000，越大越精确越慢）
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"essplanner/internal/api"
	"essplanner/internal/service"
	"essplanner/internal/store"
	"essplanner/internal/store/memory"
	"essplanner/internal/store/pg"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	port := env("PORT", "8080")
	staticDir := env("STATIC_DIR", "./static")
	tz := env("PLAN_TZ", "Asia/Shanghai")
	gridPoints, _ := strconv.Atoi(env("GRID_POINTS", "10000"))

	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Fatalf("无效时区 %s: %v", tz, err)
	}

	var st store.Store
	switch env("STORE", "pg") {
	case "memory":
		log.Println("使用内存存储（仅演示，重启丢数据）")
		st = memory.New()
	default:
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			log.Fatal("STORE=pg 时必须设置 DATABASE_URL")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pgStore, err := pg.New(ctx, dsn)
		if err != nil {
			log.Fatal(err)
		}
		st = pgStore
		log.Println("已连接 PostgreSQL")
	}
	defer st.Close()

	svc := service.New(st, loc, gridPoints)
	router := api.NewRouter(svc, staticDir)

	srv := &http.Server{Addr: ":" + port, Handler: router}
	go func() {
		log.Printf("监听 :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("已退出")
}
