#!/usr/bin/env bash
# Local fallback for what .github/workflows/release.yml does on the runner.
# Run from the repo root:
#
#     scripts/build-release.sh v0.1.0
#
# Produces dist/cfmd-<goos>-<goarch>.{tar.gz,zip} plus .sha256 files.

set -euo pipefail

version="${1:-}"
if [[ -z "$version" ]]; then
  echo "usage: $0 <version-tag>" >&2
  exit 1
fi

rm -rf dist
mkdir -p dist

targets=(
  darwin/amd64
  darwin/arm64
  linux/amd64
  linux/arm64
  windows/amd64
  windows/arm64
)

for target in "${targets[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  ext=""
  if [[ "$goos" == "windows" ]]; then ext=".exe"; fi
  binary="cfmd${ext}"
  echo "==> $goos/$goarch"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${version}" \
    -o "dist/${binary}" \
    ./cmd/cfmd
  archive_name="cfmd-${goos}-${goarch}"
  (
    cd dist
    cp ../README.md README.md
    cp ../LICENSE LICENSE
    if [[ "$goos" == "windows" ]]; then
      zip -q "${archive_name}.zip" "${binary}" README.md LICENSE
    else
      tar czf "${archive_name}.tar.gz" "${binary}" README.md LICENSE
    fi
    rm -f "${binary}" README.md LICENSE
    if [[ "$goos" == "windows" ]]; then
      shasum -a 256 "${archive_name}.zip" > "${archive_name}.zip.sha256"
    else
      shasum -a 256 "${archive_name}.tar.gz" > "${archive_name}.tar.gz.sha256"
    fi
  )
done

echo
echo "Built:"
ls -la dist/
