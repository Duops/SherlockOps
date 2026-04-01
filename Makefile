VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X github.com/shchepetkov/sherlockops/internal/version.Version=$(VERSION)

.PHONY: build test lint clean docker

build:
	cd src && CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o ../bin/sherlockops ./cmd/sherlockops

test:
	cd src && go test ./...

lint:
	cd src && go vet ./...

clean:
	rm -rf bin/

docker:
	docker build --build-arg VERSION=$(VERSION) -t sherlockops:$(VERSION) .
