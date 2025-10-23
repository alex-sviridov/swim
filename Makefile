.PHONY: test build run clean

IMAGE_NAME := swim
CONTAINER_NAME := swim-app

test:
	docker-compose -f internal/integration/docker-compose.test.yml up -d; \
	go test ./...; \
	docker-compose -f internal/integration/docker-compose.test.yml down;

build:
	go build -o bin/swim ./cmd/swim

run: build
	./bin/swim --verbose

clean:
	rm bin/*