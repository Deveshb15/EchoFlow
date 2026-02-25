.PHONY: run test fmt tidy vet lint vulncheck

run:
	go run ./cmd/echoflow-api

test:
	go test ./...

fmt:
	gofmt -w $(shell find . -name '*.go' -type f)

tidy:
	go mod tidy

vet:
	go vet ./...

lint:
	golangci-lint run

vulncheck:
	govulncheck ./...
