.PHONY: benchmarks coverage dependency-check format-check fuzz-seeds test-integration test-race test-unit vet verify workflow-check

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
	./scripts/test-coverage-report.sh

test-unit:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

test-integration:
	go test -tags=integration -count=1 ./...

fuzz-seeds:
	go test ./pkg/cdc ./pkg/common/http/request ./pkg/common/websocket ./pkg/mq/forge -run '^Fuzz' -count=1

coverage:
	./scripts/verify-coverage.sh

benchmarks:
	go test -tags=benchmark -run=^$$ -bench=. -benchmem -benchtime=500ms -count=20 ./...

verify: format-check vet dependency-check workflow-check test-unit fuzz-seeds
