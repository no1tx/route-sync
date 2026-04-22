#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
go run ./cmd/route-sync check --config examples/configs/local-dev.yaml
