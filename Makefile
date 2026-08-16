.PHONY: build test run fmt vet tidy docker-build docker-run

PORT ?= 51108

build:
	go build -o bin/arcticfreight ./cmd/server

test:
	go test -timeout=120s -count=1 ./...

run: build
	./bin/arcticfreight

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

docker-build:
	docker build -t arcticfreight .

docker-run:
	docker run --rm -p $(PORT):51108 arcticfreight
