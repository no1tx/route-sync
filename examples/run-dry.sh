#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
go run ./cmd/route-sync apply --config examples/configs/local-dev.yaml --dry-run
