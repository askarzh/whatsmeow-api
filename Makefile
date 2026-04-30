.PHONY: build run test tidy fmt vet lint clean

BIN := bin/whatsmeow-api

build:
	mkdir -p bin
	go build -o $(BIN) ./cmd/whatsmeow-api

run:
	go run ./cmd/whatsmeow-api serve

test:
	go test ./...

tidy:
	go mod tidy

fmt:
	gofmt -s -w .

vet:
	go vet ./...

lint:
	staticcheck ./...

clean:
	rm -rf bin
