BINARY := yomibako

.PHONY: build test vet check clean

build:
	go build -o $(BINARY) ./cmd/yomibako

test:
	go test ./...

vet:
	go vet ./...

check: vet test

clean:
	rm -f $(BINARY)
