# syntax=docker/dockerfile:1

# ---- 构建阶段 ----
FROM golang:1.22 AS builder
WORKDIR /src
# 先复制依赖描述，利用层缓存
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/waveguide-service ./cmd/server

# ---- 测试阶段：可在容器内直接运行自动化测试 ----
# 用法：
#   docker build --target test -t waveguide-service-test .
#   docker run --rm waveguide-service-test
FROM golang:1.22 AS test
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 默认入口即全部测试（含竞态检测）
CMD ["go", "test", "-race", "-v", "./..."]

# ---- 运行阶段：最小化无 shell 镜像，固定监听 8080 ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/waveguide-service /waveguide-service
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/waveguide-service"]
