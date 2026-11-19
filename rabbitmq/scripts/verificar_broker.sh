#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
RABBITMQ_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

cd "${RABBITMQ_DIR}"

if [[ ! -f ".env" ]]; then
  echo "ERROR: no existe rabbitmq/.env"
  echo "Copia .env.example a .env y configura las credenciales."
  exit 1
fi

echo "== Estado de los servicios =="
docker compose ps

echo
echo "== Comprobación del contenedor =="
if ! docker compose ps --status running --services | grep -qx "rabbitmq"; then
  echo "ERROR: el servicio rabbitmq no está ejecutándose."
  exit 1
fi

echo
echo "== Healthcheck del broker =="
docker compose exec -T rabbitmq rabbitmq-diagnostics -q ping

echo
echo "OK: RabbitMQ está activo y responde correctamente."
