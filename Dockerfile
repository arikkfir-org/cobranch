# Image devhub: the one binary, which every workload runs as a subcommand, with git, which the Git server and the push
# worker run. Built from the repository's root:
#
#   docker build --build-arg VERSION=v1.2.3 -t devhub:v1.2.3 .
FROM docker.io/library/golang:1.27.1-alpine3.24@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY db/ db/
COPY internal/ internal/
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/arikkfir-org/devhub/internal/system/buildinfo.version=${VERSION}" \
      -o /out/devhub ./cmd/devhub

FROM docker.io/library/alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN apk add --no-cache ca-certificates git
COPY --from=build /out/devhub /usr/local/bin/devhub
# Not root: the chart's pods run as this user and refuse root.
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/devhub"]
CMD ["version"]
