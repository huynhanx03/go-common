.PHONY: benchmarks coverage dependency-check format-check fuzz-seeds test test-integration test-race vet verify verify-docs workflow-check

format-check:
	test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))"

vet:
	go vet ./...

dependency-check:
	go mod verify
	go mod tidy -diff

workflow-check:
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
	./scripts/verify-workflows.sh

test:
	go test ./...

test-race:
	go test -race ./...

test-integration:
	./scripts/test-integration.sh

fuzz-seeds:
	go test ./pkg/cdc ./pkg/common/http/request ./pkg/common/websocket ./pkg/mq/forge -run '^Fuzz' -count=1

verify-docs:
	./scripts/verify-docs.sh

coverage:
	./scripts/verify-coverage.sh

benchmarks:
	./scripts/run-benchmarks.sh

verify: format-check vet dependency-check workflow-check test fuzz-seeds verify-docs
