FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
RUN go build -o /out/agent-factory ./cmd/agent-factory
RUN go build -o /out/agent-factory-admin ./cmd/agent-factory-admin

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/agent-factory /app/agent-factory
COPY --from=build /out/agent-factory-admin /app/agent-factory-admin
ENTRYPOINT ["/app/agent-factory"]
