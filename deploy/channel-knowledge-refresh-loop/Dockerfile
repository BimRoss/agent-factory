# Upstream geeemoney/employee-factory refresh image is often distroless (no /bin/sh); copy the binary
# into bookworm-slim so compose can run a sleep loop. Build is linux/amd64 to match shipped tags.
ARG UPSTREAM_IMAGE=geeemoney/employee-factory:0.15.23
FROM --platform=linux/amd64 ${UPSTREAM_IMAGE} AS upstream
FROM --platform=linux/amd64 debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*
COPY --from=upstream /app/channel-knowledge-refresh /app/channel-knowledge-refresh
COPY deploy/channel-knowledge-refresh-loop/loop.sh /loop.sh
RUN chmod +x /loop.sh
ENTRYPOINT ["/bin/sh", "/loop.sh"]
