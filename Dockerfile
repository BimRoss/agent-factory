# Build with context = parent directory that contains agent-factory/, shared-contracts/, skill-factory/
# Example: cd .. && docker build -f agent-factory/Dockerfile -t agent-factory:dev .
# CI checks out all three repos as siblings (see .github/workflows/agent-factory-images.yml).
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY agent-factory/go.mod agent-factory/go.sum ./
RUN go mod download
COPY agent-factory/ ./
RUN go build -o /out/agent-factory ./cmd/agent-factory
RUN go build -o /out/agent-factory-admin ./cmd/agent-factory-admin

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/agent-factory /app/agent-factory
COPY --from=build /out/agent-factory-admin /app/agent-factory-admin
COPY shared-contracts /workspace/shared-contracts
COPY skill-factory /workspace/skill-factory
ENV SHARED_CONTRACTS_DIR=/workspace/shared-contracts
ENV SKILL_FACTORY_DIR=/workspace/skill-factory
ENV MEMORY_BANK_FILE=/workspace/shared-contracts/memory-bank.v1.json
ENTRYPOINT ["/app/agent-factory"]
