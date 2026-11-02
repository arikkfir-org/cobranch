# Seeds the Docker daemon's Go cache mounts, which the build stage of the repository's Dockerfile mounts by the same
# ids, with main's Go caches (scripts/go-cache-seed.sh). Built from .tekton, with the access token as secret gcs-token.
FROM docker.io/curlimages/curl:8.22.0@sha256:58adaa4e8dca9c988bae2aba4ab3434a0bb2da16bbe3f92dec39ec7785166777
USER root
RUN --mount=type=bind,source=scripts/go-cache-restore.sh,target=/go-cache-restore.sh \
    --mount=type=secret,id=gcs-token \
    --mount=type=cache,id=/root/.cache/go-build,target=/go-cache/build \
    --mount=type=cache,id=/go/pkg/mod,target=/go-cache/mod \
    --mount=type=cache,id=golangci-lint,target=/go-cache/lint \
    sh /go-cache-restore.sh
