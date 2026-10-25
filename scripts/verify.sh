#!/usr/bin/env bash
set -euo pipefail
go test -race ./...
go vet ./...
test -z "$(gofmt -l cmd internal)"
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/sourceegg ./cmd/sourceegg
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/sourcenode ./cmd/sourcenode
bash -n scripts/install-node.sh scripts/smoke-container.sh scripts/test-install-node.sh
echo 'Linux test, race, vet, formatting, build, and shell checks passed'
