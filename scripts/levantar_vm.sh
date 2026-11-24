#!/usr/bin/env bash
# =============================================================
# levantar_vm.sh
# Lanza los servicios de esta VM usando Docker Compose con el
# profile adecuado.
#
# Formas de uso:
#
#   1) Pasando el ID del nodo como primer argumento:
#      bash scripts/levantar_vm.sh 1
#      bash scripts/levantar_vm.sh 2 -d
#      bash scripts/levantar_vm.sh 3 --build -d   (coordinador + RabbitMQ)
#
#   2) Sin argumentos (lee NODE_ID automáticamente desde .env):
#      bash scripts/levantar_vm.sh
#      bash scripts/levantar_vm.sh -d
#      bash scripts/levantar_vm.sh --build
#
# Requisito: .env en la raíz del repositorio con la configuración Tailscale.
#   cp .env.vm.example .env
# =============================================================
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
ENV_FILE="${REPO_ROOT}/.env"

NODE_ID=""

# Si el primer argumento es un número (ej. 1, 2, 3), se toma como NODE_ID
if [[ $# -gt 0 && "$1" =~ ^[0-9]+$ ]]; then
  NODE_ID="$1"
  shift # consumir el argumento para que el resto vaya a docker compose
fi

# Si no se pasó por argumento, leer NODE_ID desde .env
if [[ -z "${NODE_ID}" ]]; then
  if [[ ! -f "${ENV_FILE}" ]]; then
    echo "ERROR: no se pasó el ID de nodo y tampoco existe .env en ${REPO_ROOT}" >&2
    echo "       Uso con parámetro: bash scripts/levantar_vm.sh <ID_NODO> [opciones docker compose]" >&2
    echo "       O copia la plantilla: cp .env.vm.example .env" >&2
    exit 1
  fi

  NODE_ID=$(grep -E '^NODE_ID=' "${ENV_FILE}" \
    | head -1 \
    | cut -d'=' -f2 \
    | tr -d '[:space:]\r')
fi

if [[ -z "${NODE_ID}" ]]; then
  echo "ERROR: No se pudo determinar el NODE_ID (ni por parámetro ni en .env)." >&2
  exit 1
fi

PROFILE="nodo${NODE_ID}"

echo "=========================================================="
echo " VM SD-Taller2 → Nodo ID: ${NODE_ID} | Profile: ${PROFILE}"
if [[ "${NODE_ID}" == "3" ]]; then
  echo " [ROL: Coordinador] → Se levantarán mongod + rabbitmq + mongo-init + nodo3"
else
  echo " [ROL: Worker/Follower] → Se levantarán mongod + nodo${NODE_ID}"
fi
echo "=========================================================="

cd "${REPO_ROOT}"

# Ejecutar docker compose con el profile correspondiente y los flags adicionales
exec docker compose --profile "${PROFILE}" up "$@"
