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

required_queues=(
  "metrics"
  "allocations"
  "failover"
)

echo "== Colas configuradas en RabbitMQ =="

queue_output="$(
  docker compose exec -T rabbitmq \
    rabbitmqctl list_queues \
    -p "/" \
    name durable auto_delete messages_ready messages_unacknowledged
)"

echo "${queue_output}"

echo
echo "== Validación de colas requeridas =="

for queue in "${required_queues[@]}"; do
  if ! printf '%s\n' "${queue_output}" | awk -v expected="${queue}" '$1 == expected { found = 1 } END { exit(found ? 0 : 1) }'; then
    echo "ERROR: no existe la cola requerida: ${queue}"
    exit 1
  fi

  echo "OK: cola ${queue}"
done

echo
echo "OK: las tres colas requeridas existen."
