#!/usr/bin/env bash
# Inspect production dependencies, excluding test fixtures and Forge's IPC client
# as an engine root. That client must never appear transitively in the engine.
set -euo pipefail
cd "$(dirname "$0")/.."
export GOWORK=off GOFLAGS=-mod=readonly CGO_ENABLED=0 GOTOOLCHAIN=local GOOS=linux

listed=$(go list -mod=readonly ./pkg/factory/... ./pkg/store ./pkg/collaboration)
engine_roots=()
for package in $listed; do
  if [[ "$package" != github.com/levitateos/factory/pkg/factory/client ]]; then
    engine_roots+=("$package")
  fi
done
dependencies=$(go list -mod=readonly -deps "${engine_roots[@]}")
# GitHub belongs in the native server's provider composition, never the engine.
for dependency in $dependencies; do
  if [[ "$dependency" == github.com/google/go-github/* ||
        "$dependency" == github.com/levitateos/factory/pkg/github ||
        "$dependency" == github.com/levitateos/factory/pkg/github/* ]]; then
    printf 'Factory engine has a GitHub provider dependency: %s\n' "$dependency" >&2
    exit 1
  fi
done
native_dependencies=$(go list -mod=readonly -deps ./cmd/soda-factory-server)
dependencies+=$'\n'"$native_dependencies"
for dependency in $dependencies; do
  if [[ "$dependency" == forgejo.org || "$dependency" == forgejo.org/* ||
        "$dependency" =~ ^github\.com/levitateos/(sodaos|factory)/internal/ ||
        "$dependency" =~ ^github\.com/levitateos/factory/pkg/(forgejo|host|web|identity/client|factory/client|unixhttp)(/|$) ]]; then
    printf 'Factory engine has a provider/host/private transport dependency: %s\n' "$dependency" >&2
    exit 1
  fi
done
printf 'Factory engine/store exclude provider SDKs; native command excludes appliance adapters and private clients.\n'
