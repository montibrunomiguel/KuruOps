.PHONY: build run-api run-ingest run-worker test vet fmt

build:
	go build ./...

run-api:
	go run ./cmd/api

run-ingest:
	go run ./cmd/ingest

run-worker:
	go run ./cmd/worker

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .
