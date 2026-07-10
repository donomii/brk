#!/bin/sh
set -eu
go run ./cmd/brk-demo
go run ./cmd/brk-lossy-demo
go run ./cmd/brk-nat-demo
