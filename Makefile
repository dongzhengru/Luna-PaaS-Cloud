.PHONY: test build dev format format-check
test: format-check
	cd backend && go test ./...
	cd frontend && npm run typecheck
build:
	cd backend && go build ./cmd/server
	cd frontend && npm run build
dev:
	docker compose up --build
format:
	gofmt -w backend/cmd/server/*.go backend/internal/*/*.go
	cd frontend && npm run format
format-check:
	test -z "$$(gofmt -l backend/cmd/server/*.go backend/internal/*/*.go)"
	cd frontend && npm run format:check
