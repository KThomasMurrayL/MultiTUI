#!/usr/bin/env bash
# Build static macOS/Linux tarballs; no publishing or machine-wide install.
set -euo pipefail

version="${1:-}"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]; then
  echo "Usage: bash scripts/release.sh <version>, e.g. 0.1.0" >&2
  exit 1
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
go_binary="${GO:-go}"
"$go_binary" test ./...
"$go_binary" vet ./...
mkdir -p dist
stage="$(mktemp -d "${TMPDIR:-/tmp}/mt-release.XXXXXX")"
trap 'rm -rf "$stage"' EXIT

for target_os in darwin linux; do
  for target_arch in amd64 arm64; do
    archive="mt_${version}_${target_os}_${target_arch}"
    mkdir -p "$stage/$archive"
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" "$go_binary" build \
      -trimpath -ldflags "-s -w -X main.version=$version" \
      -o "$stage/$archive/mt" ./cmd/mt
    cp README.md config.example.yaml "$stage/$archive/"
    COPYFILE_DISABLE=1 tar -czf "dist/$archive.tar.gz" -C "$stage/$archive" mt README.md config.example.yaml
  done
done

(
  cd dist
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "mt_${version}_"*.tar.gz > "mt_${version}_checksums.txt"
  else
    shasum -a 256 "mt_${version}_"*.tar.gz > "mt_${version}_checksums.txt"
  fi
)
echo "Built macOS/Linux amd64/arm64 archives and checksums in dist/."
