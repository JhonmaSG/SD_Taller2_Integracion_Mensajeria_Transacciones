# syntax=docker/dockerfile:1

# ---------------------------------------------------------------
# Etapa 1: compilar el binario estático del nodo (cmd/main.go)
# ---------------------------------------------------------------
FROM golang:1.24-bookworm AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

# Descargar dependencias primero para aprovechar la caché de capas.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copiar solo el código Go. Así .env, scripts, README y demás archivos
# no entran en la imagen.
COPY agent ./agent
COPY election ./election
COPY cmd ./cmd
COPY internal ./internal

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/nodo ./cmd

# ---------------------------------------------------------------
# Etapa 2: imagen de ejecución basada en Ubuntu 26.04
# ---------------------------------------------------------------
FROM ubuntu:26.04

# Certificados raíz para conexiones TLS (MongoDB, RabbitMQ con amqps).
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Usuario sin privilegios de sistema.
RUN useradd --system --uid 10001 --no-create-home --shell /usr/sbin/nologin nodo

WORKDIR /app

COPY --from=builder /out/nodo /app/nodo

# El nodo lee el archivo .env desde el directorio de trabajo (/app) si existe.
# Las variables también se pueden pasar con --env-file al ejecutar el contenedor.
USER 10001:10001

# Puerto gRPC por defecto del nodo (NODE_PORT, valor por defecto 5000).
EXPOSE 5000

ENTRYPOINT ["/app/nodo"]
