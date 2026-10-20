# Default recipe runs generate, build, and test
default: generate build test

# Run code generators (protobuf schemas, Cloudflare CIDRs, etc.)
generate:
    go generate ./...

# Vet and compile all packages
build:
    go vet ./...
    go build ./...

# Run the test suite
test:
    go test -v ./...
