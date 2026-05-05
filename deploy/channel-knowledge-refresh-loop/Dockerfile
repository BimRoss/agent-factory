# Local compose image: build channel-knowledge-refresh from this repo (no employee-factory image).
# Context must be the agent-factory repo root (see docker-compose.core.yml). linux/amd64 for Apple Silicon parity with CI.
FROM --platform=linux/amd64 golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/channel-knowledge-refresh ./cmd/channel-knowledge-refresh

FROM --platform=linux/amd64 debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/channel-knowledge-refresh /app/channel-knowledge-refresh
COPY deploy/channel-knowledge-refresh-loop/loop.sh /loop.sh
RUN chmod +x /loop.sh
ENTRYPOINT ["/bin/sh", "/loop.sh"]
