# ---------- 前端构建 ----------
FROM node:20-alpine AS frontend
WORKDIR /build
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# ---------- 后端编译 ----------
FROM golang:1.22-alpine AS backend
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---------- 运行层：只留二进制和静态文件 ----------
FROM alpine:3.20
RUN adduser -D -u 10001 app
COPY --from=backend /out/server /usr/local/bin/server
COPY --from=frontend /build/dist /static
ENV PORT=8080 \
    STATIC_DIR=/static \
    STORE=pg \
    PLAN_TZ=Asia/Shanghai \
    GRID_POINTS=10000 \
    GIN_MODE=release
EXPOSE 8080
USER app
CMD ["server"]
