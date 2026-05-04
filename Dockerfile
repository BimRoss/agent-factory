FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
RUN go build -o /out/agent-factory ./cmd/agent-factory

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/agent-factory /app/agent-factory
ENTRYPOINT ["/app/agent-factory"]
