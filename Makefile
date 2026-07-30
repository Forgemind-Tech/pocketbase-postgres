# version stamped into the binary; override with `make build VERSION=v1.2.3`
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "(untracked)")

# -s -w strips the symbol table and DWARF data, -trimpath removes local
# filesystem paths so the build does not depend on where it was compiled
LDFLAGS := -s -w -X github.com/pocketbase/pocketbase.Version=$(VERSION)
BUILDFLAGS := -trimpath -ldflags "$(LDFLAGS)"

# CGO is disabled because the whole stack (including the pgx Postgres driver) is
# pure Go, which keeps the binary statically linked and portable
build:
	CGO_ENABLED=0 go build $(BUILDFLAGS) -o .builds/pocketbase ./examples/base

# common deployment target from any host
build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(BUILDFLAGS) -o .builds/pocketbase-linux-amd64 ./examples/base

build-linux-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(BUILDFLAGS) -o .builds/pocketbase-linux-arm64 ./examples/base

lint:
	golangci-lint run -c ./golangci.yml ./...

test:
	go test ./... -v --cover

jstypes:
	go run ./plugins/jsvm/internal/types/types.go

test-report:
	go test ./... -v --cover -coverprofile=coverage.out
	go tool cover -html=coverage.out
