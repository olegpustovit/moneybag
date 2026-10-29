.PHONY: ui backend build check
ui:
	cd ui && bun install --frozen-lockfile && bun run lint && bun run build
	rsync -a --delete --exclude=.keep ui/dist/ backend/cmd/api/handlers/dist/
backend:
	cd backend && go test ./... && go vet ./...
build: ui
	cd backend && go build -o bin/moneybag-local ./cmd/api
check: ui backend
	cd ui && bun run test
