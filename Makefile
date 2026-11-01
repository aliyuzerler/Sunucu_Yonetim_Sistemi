# Server Control — monorepo kök görevleri (Faz 01)
# Tam geliştirme talimatı: docs/operations/development-environment.md

GO ?= go
CD_CP = cd backend/control-plane

.PHONY: help backend-build backend-test backend-race backend-vet backend-fmt \
        web-install web-lint web-typecheck web-test web-build web-e2e web-e2e-install web-e2e-real \
        agent-build agent-test agent-vet \
        proto-tools proto proto-check \
        compose-up compose-down compose-ps migrate-up migrate-status migrate-down

help:
	@echo "Backend:  make backend-build | backend-test | backend-race | backend-vet | backend-fmt"
	@echo "Frontend: make web-install | web-lint | web-typecheck | web-test | web-build | web-e2e"
	@echo "DB:       make compose-up | compose-ps | migrate-up | migrate-status | migrate-down"
	@echo "Agent:    make agent-build | agent-test | agent-vet"
	@echo "Proto:    make proto-tools | proto | proto-check"

backend-build:
	$(CD_CP) && $(GO) build ./...

backend-test:
	$(CD_CP) && $(GO) test ./...

backend-race:
	$(CD_CP) && $(GO) test -race ./...

backend-vet:
	$(CD_CP) && $(GO) vet ./...

backend-fmt:
	$(CD_CP) && $(GO) fmt ./... && $(GO) vet ./...

web-install:
	cd web && npm ci

web-lint:
	cd web && npm run lint

web-typecheck:
	cd web && npm run typecheck

web-test:
	cd web && npm test

web-build:
	cd web && npm run build

# E2E (Playwright): önce build; mock API ile lokal, gerçek backend ile CI.
web-e2e-install:
	cd web && npx playwright install chromium

web-e2e: web-build
	cd web && npm run e2e

web-e2e-real: web-build
	cd web && E2E_REAL=1 API_PROXY_TARGET=$${API_PROXY_TARGET:-http://127.0.0.1:8080} npm run e2e

agent-build:
	cd agent && $(GO) build ./...

agent-test:
	cd agent && $(GO) test ./...

agent-vet:
	cd agent && $(GO) vet ./...

# Protokol araçları: pinli buf + Go plugin'leri (Go module proxy checksum'lu).
PROTO_BUF          := github.com/bufbuild/buf/cmd/buf@v1.75.0
PROTO_GEN_GO       := google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
PROTO_GEN_GO_GRPC  := google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1

proto-tools:
	$(GO) install $(PROTO_BUF)
	$(GO) install $(PROTO_GEN_GO)
	$(GO) install $(PROTO_GEN_GO_GRPC)

proto:
	cd proto && buf lint && buf generate

# Kırılma kapısı: üretilen kod taze değilse başarısız olur (CI'da zorlanır).
proto-check: proto
	git diff --exit-code -- gen/proto

compose-up:
	cd deploy && docker compose up -d && docker compose ps

compose-down:
	cd deploy && docker compose down

compose-ps:
	cd deploy && docker compose ps

# Migration komutları CP_DATABASE_URL ortam değişkenini kullanır:
#   export CP_DATABASE_URL="postgres://...?sslmode=disable"
migrate-up:
	$(CD_CP) && $(GO) run ./cmd/migrate up

migrate-status:
	$(CD_CP) && $(GO) run ./cmd/migrate status

migrate-down:
	$(CD_CP) && $(GO) run ./cmd/migrate down
