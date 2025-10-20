.PHONY: test build run clean

IMAGE_NAME := swim
CONTAINER_NAME := swim-app

test:
	go test -short ./...

build:
	go build -o bin/swim ./cmd/swim

run: build
	./bin/swim --verbose

clean:
	rm bin/*