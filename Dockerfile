# syntax=docker/dockerfile:1

# ---- 阶段 1：构建前端 ----
FROM node:20-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# ---- 阶段 2：编译后端（嵌入前端产物） ----
FROM golang:1.22-alpine AS backend
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# 用前端构建产物替换嵌入目录中的占位文件
RUN rm -f web/dist/.placeholder
COPY --from=frontend /app/frontend/dist/ ./web/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bess ./cmd/server

# ---- 阶段 3：运行层，只保留二进制（静态文件已嵌入二进制） ----
FROM scratch
COPY --from=backend /out/bess /bess
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/bess"]
