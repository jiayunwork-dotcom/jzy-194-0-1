// Package web 通过 go:embed 携带前端构建产物（frontend/dist 拷贝到 web/dist）。
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
