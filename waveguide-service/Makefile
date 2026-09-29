.PHONY: build test test-race run vet fmt docker-build docker-test

build:
	go build -o bin/waveguide-service ./cmd/server

test:
	go test ./...

test-race:
	go test -race -count=1 ./...

run:
	go run ./cmd/server

vet:
	go vet ./...

fmt:
	gofmt -w .

# 构建运行镜像（固定监听 8080）
docker-build:
	docker build -t waveguide-service .

# 在容器内跑全部自动化测试（含竞态检测）
docker-test:
	docker build --target test -t waveguide-service-test .
	docker run --rm waveguide-service-test
