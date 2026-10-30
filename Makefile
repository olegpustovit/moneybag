.PHONY: ui backend build check test-integration
ui:
	cd ui && bun install --frozen-lockfile && bun run lint && bun run build
	rsync -a --delete --exclude=.keep ui/dist/ backend/cmd/api/handlers/dist/
backend:
	cd backend && go test ./... && go vet ./...
build: ui
	cd backend && go build -o bin/moneybag-local ./cmd/api
check: ui backend
	cd ui && bun run test
# In-memory Mongo: seconds rather than minutes over the network. Needs Docker.
test-integration:
	docker rm -f moneybag-test-mongo >/dev/null 2>&1 || true
	docker run -d --name moneybag-test-mongo --ulimit nofile=64000:64000 --tmpfs /data/db:size=1g -p 127.0.0.1:27099:27017 mongo:8 --wiredTigerCacheSizeGB 0.5 >/dev/null
	until docker exec moneybag-test-mongo mongosh --quiet --eval 'db.runCommand({ping:1}).ok' >/dev/null 2>&1; do sleep 0.2; done
	cd backend && MONEYBAG_TEST_MONGO_URI=mongodb://127.0.0.1:27099/moneybag_test_local go test -timeout 120s -count=1 ./...; status=$$?; docker rm -f moneybag-test-mongo >/dev/null; exit $$status
