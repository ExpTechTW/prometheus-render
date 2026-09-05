#!/usr/bin/env bash
# Cross-compiles prometheus-render for every supported target and packages each
# build with the documents that go with it.
#
# The drawing is pure Go with no cgo, so every target cross-compiles from one
# machine. That is why this is a loop rather than a matrix of CI runners.
#
#   hack/build-release.sh v1.0.0 [outdir]
set -euo pipefail

version="${1:-dev}"
out="${2:-dist}"
pkg=./cmd/prometheus-render

# GOOS/GOARCH, plus an optional GOARM after a second slash.
targets=(
  linux/amd64      linux/arm64      linux/arm/7   linux/arm/6
  linux/386        linux/riscv64    linux/ppc64le linux/s390x
  darwin/amd64     darwin/arm64
  windows/amd64    windows/arm64    windows/386
  freebsd/amd64    freebsd/arm64
  openbsd/amd64    openbsd/arm64
  netbsd/amd64
)

sha256() { command -v sha256sum >/dev/null && sha256sum "$@" || shasum -a 256 "$@"; }

rm -rf "$out"
mkdir -p "$out"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

for target in "${targets[@]}"; do
  IFS=/ read -r os arch arm <<<"$target"
  name="${arch}"
  [ -n "${arm:-}" ] && name="armv${arm}"

  bin="prometheus-render"
  [ "$os" = windows ] && bin="prometheus-render.exe"

  stage="$work/prometheus-render_${version}_${os}_${name}"
  mkdir -p "$stage"

  # -s -w drop the symbol table and DWARF; the version is the tag being built.
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOARM="${arm:-}" \
    go build -trimpath -ldflags "-s -w -X main.version=${version}" \
    -o "$stage/$bin" "$pkg"

  cp README.md README-EN.md LICENSE NOTICE site.example.yml "$stage/"

  if [ "$os" = windows ]; then
    (cd "$work" && zip -qr "$(basename "$stage").zip" "$(basename "$stage")")
    mv "$work/$(basename "$stage").zip" "$out/"
  else
    tar -czf "$out/$(basename "$stage").tar.gz" -C "$work" "$(basename "$stage")"
  fi
  printf '  %-34s %s\n' "$os/$name" "$(du -h "$stage/$bin" | cut -f1)"
done

(cd "$out" && sha256 ./* > SHA256SUMS)
echo "wrote $(ls -1 "$out" | wc -l | tr -d ' ') files to $out/"
