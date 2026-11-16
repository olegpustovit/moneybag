.PHONY: ui backend build check test-integration test-identity-provider
ui:
	cd ui && bun install --frozen-lockfile && bun run lint && bun run build
	rsync -a --delete --exclude=.keep ui/dist/ backend/cmd/api/handlers/dist/
backend:
	cd backend && go test ./... && go vet ./...
build: ui
	cd backend && go build -o bin/moneybag-local ./cmd/api
check: ui backend
	cd ui && bun run test
# Disposable PostgreSQL with fast test-only durability settings. Needs Docker.
test-integration:
	docker rm -f moneybag-test-pg >/dev/null 2>&1 || true
	docker run -d --name moneybag-test-pg --tmpfs /var/lib/postgresql:size=1g -e POSTGRES_PASSWORD=test -p 127.0.0.1:55432:5432 postgres:18 -c fsync=off -c synchronous_commit=off -c full_page_writes=off >/dev/null
	timeout 30s bash -c 'until docker exec moneybag-test-pg pg_isready -U postgres >/dev/null 2>&1; do sleep 0.2; done'
	cd backend && MONEYBAG_TEST_DATABASE_URL=postgres://postgres:test@127.0.0.1:55432/postgres go test -timeout 120s ./...; status=$$?; docker rm -f moneybag-test-pg >/dev/null; exit $$status

# Requires a disposable PostgreSQL server and the sibling Identity owned e2e runner.
test-identity-provider: ui
	@test -n "$$MONEYBAG_TEST_DATABASE_URL" || { echo 'Set MONEYBAG_TEST_DATABASE_URL to an owned disposable PostgreSQL server.' >&2; exit 1; }
	@test -x ../identity/target/debug/identity || { echo 'Build the sibling Identity debug binary first.' >&2; exit 1; }
	cd e2e && npm ci --ignore-scripts
	ID_E2E_SCENARIO="$(CURDIR)/e2e/identity-provider-scenario.mjs" node ../identity/e2e/run.mjs
