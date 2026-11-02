# Image cobranch: the one binary, which every workload runs as a subcommand, with git, which the Git server and the push
# worker run. Built from the repository's root:
#
#   docker build --build-arg VERSION=v1.2.3 -t cobranch:v1.2.3 .
FROM docker.io/library/golang:1.27.1-alpine3.24@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/ cmd/
COPY db/ db/
COPY internal/ internal/
ARG VERSION=dev
# Go's module and build caches persist across builds as cache mounts, so a rebuild downloads and compiles only what
# changed: the build downloads only the modules it compiles.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X cobranch.dev/internal/system/buildinfo.version=${VERSION}" \
      -o /out/cobranch ./cmd/cobranch

FROM docker.io/library/alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN apk add --no-cache ca-certificates git
COPY --from=build /out/cobranch /usr/local/bin/cobranch
# Not root: the chart's pods run as this user and refuse root.
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/cobranch"]
CMD ["version"]
