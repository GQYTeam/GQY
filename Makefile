SHELL := /bin/sh
APP := app
GO ?= go

.PHONY: help check fmt vet lint test race cover build run demo js-check ui-smoke smoke-live tidy clean

help:
	@printf '%s\n' \
	  'make check   gofmt + vet + test + build' \
	  'make fmt     格式化 Go 代码' \
	  'make vet     go vet' \
	  'make lint    golangci-lint（需自行安装）' \
	  'make test    单元测试' \
	  'make race    竞态检测' \
	  'make cover    覆盖率报告' \
	  'make build    构建 app/bin/gqy-server' \
	  'make run      启动服务' \
	  'make demo     一键启动 UI（默认接本机 Magpie）' \
	  'make js-check JS 语法检查' \
	  'make ui-smoke 启动临时服务并用 headless Chrome 冒烟 UI' \
	  'make smoke-live 对上游做端到端冒烟（默认本机 Magpie）' \
	  'make tidy     go mod tidy' \
	  'make clean    清理构建产物'

check: fmt vet test build

fmt:
	cd $(APP) && gofmt -w ./cmd ./internal

vet:
	cd $(APP) && $(GO) vet ./...

lint:
	cd $(APP) && golangci-lint run

test:
	cd $(APP) && $(GO) test ./...

race:
	cd $(APP) && $(GO) test -race ./...

cover:
	cd $(APP) && $(GO) test -coverprofile=coverage.out ./... && $(GO) tool cover -func=coverage.out

build:
	mkdir -p $(APP)/bin
	cd $(APP) && $(GO) build -trimpath -ldflags "-s -w" -o bin/gqy-server ./cmd/gqy-server
	cd $(APP) && $(GO) build -trimpath -ldflags "-s -w" -o bin/gqy-clean ./cmd/gqy-clean
	cd $(APP) && $(GO) build -trimpath -ldflags "-s -w" -o bin/gqy-eval ./cmd/gqy-eval
	cd $(APP) && $(GO) build -trimpath -ldflags "-s -w" -o bin/gqy-run ./cmd/gqy-run

run:
	cd $(APP) && $(GO) run ./cmd/gqy-server


demo:
	./scripts/demo.sh

js-check:
	./scripts/check-js.sh

ui-smoke:
	./scripts/ui-smoke.sh

smoke-live:
	./scripts/smoke-upstream.sh



tidy:
	cd $(APP) && $(GO) mod tidy

clean:
	rm -rf $(APP)/bin $(APP)/coverage.out coverage.out dist
