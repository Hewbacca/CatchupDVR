.PHONY: test dev-backend dev-web build

test:
	cd backend && go test ./...
	cd web && npm test

dev-backend:
	cd backend && go run ./cmd/catchupd

dev-web:
	cd web && npm run dev

build:
	cd web && npm run build
	cd backend && go build ./cmd/catchupd

