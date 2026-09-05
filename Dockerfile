# Builds the image published as ghcr.io/exptechtw/prometheus-render.
#
# The drawing is pure Go with no cgo, so the result is a single static binary
# on scratch: nothing to patch, nothing to exec into.

# Built on the runner's own architecture and cross-compiled from there: pure
# Go needs no emulation, so an arm64 image costs no more than an amd64 one.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
# timetzdata embeds the zone database, so a config naming a timezone works
# without /usr/share/zoneinfo in the final image. The release binaries are
# built without it and stay smaller.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -tags timetzdata \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /prometheus-render ./cmd/prometheus-render

# scratch has no shell to create this with, and the unprivileged user below
# cannot write to /.
RUN install -d -o 65534 -g 65534 /site

FROM scratch
# Needed only for an https data source; an in-cluster one never reaches for it.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65534:65534 /site /site
COPY --from=build /prometheus-render /prometheus-render

USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/prometheus-render"]
