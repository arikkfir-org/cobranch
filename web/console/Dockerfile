# Image cobranch-console: Caddy serving the Console's static files. Built from this directory:
#
#   docker build -t cobranch-console:v1.2.3 web/console
FROM docker.io/library/caddy:2.11.7-alpine@sha256:eb4d35b7452d1ab6171fbdbe8d9275b2301dad6dad92115d91e19fb9b24d510f AS caddy
# Caddy's binary carries the capability to bind low ports, and a pod that drops every capability can't run a binary
# that carries one. The Console listens on 8080 and needs none.
RUN setcap -r /usr/bin/caddy

FROM docker.io/library/alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN apk add --no-cache ca-certificates mailcap
COPY --from=caddy /usr/bin/caddy /usr/bin/caddy
COPY Caddyfile /etc/caddy/Caddyfile
COPY public/ /srv/
# Caddy writes its instance ID and state here: a temporary directory, so the root filesystem can be read-only.
ENV XDG_CONFIG_HOME=/tmp/caddy/config XDG_DATA_HOME=/tmp/caddy/data
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/bin/caddy"]
CMD ["run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
