#!/bin/sh
# Vets and tests every Go module. Then it builds each module alone, without
# go.work: go.work lets a module import one its go.mod does not require, and
# that build is where an import across the module lines fails.
set -eu
cd "$(dirname "$0")"

mods="api hub tui cmd/neoharness"
pkgs=$(for m in $mods; do printf './%s/... ' "$m"; done)

go vet $pkgs
go test "$@" $pkgs
for m in $mods; do
	(cd "$m" && GOWORK=off go build ./...)
done
