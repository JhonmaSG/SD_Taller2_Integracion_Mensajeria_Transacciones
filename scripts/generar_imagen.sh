#!/usr/bin/env bash
#
# generar_imagen.sh
# Construye la imagen Docker del nodo (src/bully-algorithm) y la exporta a un
# archivo .tar con `docker save`. El .tar resultante se envía a los nodos de la
# tailnet con scripts/enviar_archivos.sh y se carga en cada uno con `docker load`.
#
# Uso:
#   bash scripts/generar_imagen.sh
#
# Variables de entorno:
#   IMAGE_NAME   (opcional) nombre:tag de la imagen. Por defecto: bully-nodo:latest
#   OUTPUT       (opcional) ruta del .tar de salida.
#                Por defecto: <raiz>/.runtime/images/bully-nodo.tar
#   MODULE_DIR   (opcional) ruta del módulo con el Dockerfile.
#                Por defecto: <raiz>/src/bully-algorithm
#   PLATFORM     (opcional) plataforma de build. Por defecto: linux/amd64
#
# Ejemplos:
#   bash scripts/generar_imagen.sh
#   IMAGE_NAME=bully-nodo:v1 OUTPUT=/tmp/bully.tar bash scripts/generar_imagen.sh
#
# Requisitos:
#   - Docker instalado y en ejecución en esta máquina.

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

IMAGE_NAME="${IMAGE_NAME:-bully-nodo:latest}"
MODULE_DIR="${MODULE_DIR:-${REPO_ROOT}/src/bully-algorithm}"
OUTPUT="${OUTPUT:-${REPO_ROOT}/.runtime/images/bully-nodo.tar}"
PLATFORM="${PLATFORM:-linux/amd64}"

if ! command -v docker >/dev/null 2>&1; then
  echo "ERROR: docker no está instalado o no está en el PATH." >&2
  exit 1
fi

if [[ ! -f "${MODULE_DIR}/Dockerfile" ]]; then
  echo "ERROR: no se encontró el Dockerfile en '${MODULE_DIR}'." >&2
  exit 1
fi

echo "==> Módulo:    ${MODULE_DIR}"
echo "==> Imagen:    ${IMAGE_NAME}"
echo "==> Plataforma: ${PLATFORM}"
echo "==> Salida:    ${OUTPUT}"

echo "==> Construyendo imagen..."
docker build --platform "${PLATFORM}" -t "${IMAGE_NAME}" "${MODULE_DIR}"

echo "==> Exportando imagen a ${OUTPUT} ..."
mkdir -p "$(dirname "${OUTPUT}")"
docker save -o "${OUTPUT}" "${IMAGE_NAME}"

echo "==> Listo. Imagen exportada: ${OUTPUT}"
echo
echo "    Siguiente paso — enviar a los nodos de la tailnet:"
echo "      NODES=\"nodo1@100.x.x.1 nodo2@100.x.x.2\" \\"
echo "        bash scripts/enviar_archivos.sh \"${OUTPUT}\""
echo
echo "    En cada nodo — cargar la imagen:"
echo "      docker load -i /home/<usuario>/test/$(basename "${OUTPUT}")"
