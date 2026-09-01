# syntax=docker/dockerfile:1

FROM golang:1.26.5-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/agent-studio-api ./apps/api/cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/agent-studio-worker ./apps/api/cmd/worker
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/agent-studio ./cmd/agent-studio

FROM alpine:3.23

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 agentstudio \
    && adduser -S -D -u 10001 -G agentstudio agentstudio
ENV HOME=/home/agentstudio
WORKDIR /app
COPY --from=builder --chown=10001:10001 /out/agent-studio-api /app/agent-studio-api
COPY --from=builder --chown=10001:10001 /out/agent-studio-worker /app/agent-studio-worker
COPY --from=builder --chown=10001:10001 /out/agent-studio /app/agent-studio

USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/agent-studio-api"]
