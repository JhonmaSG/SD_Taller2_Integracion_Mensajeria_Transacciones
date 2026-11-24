#!/usr/bin/env bash
#
# enviar_archivos.sh
# Copia uno o varios archivos (o carpetas) a cada máquina remota vía scp (SSH).
# Mismo patrón que src/bully-algorithm/scripts/deploy_nodos.sh, pero sin compilar:
# solo transfiere los archivos que le indiques.
#
# Uso:
#   NODES="nodo1@100.x.x.1 nodo2@100.x.x.2" \
#   bash scripts/enviar_archivos.sh archivo1 [archivo2 ...]
#
# Variables de entorno:
#   NODES        (obligatoria) lista de nodos "usuario@host" separados por espacios.
#   DEST_SUBDIR  (opcional) subcarpeta destino dentro del HOME. Por defecto: test
#                Destino final por nodo: /home/<usuario>/<DEST_SUBDIR>
#   SSH_KEY      (opcional) ruta a la llave privada. Por defecto: ~/.ssh/id_ed25519_vm
#   RECURSIVE    (opcional) "1" para copiar carpetas recursivamente (scp -r).
#
# Ejemplos:
#   NODES="nodo1@100.0.0.1 nodo2@100.0.0.2" \
#     bash scripts/enviar_archivos.sh config/datos.json
#
#   NODES="nodo1@100.0.0.1" DEST_SUBDIR=bully \
#     bash scripts/enviar_archivos.sh seed.sql .env.example
#
#   NODES="nodo1@100.0.0.1" RECURSIVE=1 \
#     bash scripts/enviar_archivos.sh docs/
#
# Requisitos:
#   - Acceso SSH por llave a cada nodo (password deshabilitado).

set -Eeuo pipefail

# Ruta a la llave privada SSH.
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_ed25519_vm}"

DEST_SUBDIR="${DEST_SUBDIR:-test}"

RECURSIVE="${RECURSIVE:-0}"

if [[ -z "${NODES:-}" ]]; then
  echo "ERROR: define la variable de entorno NODES con los nodos destino." >&2
  echo '       Ejemplo: NODES="nodo1@100.x.x.1 nodo2@100.x.x.2" bash scripts/enviar_archivos.sh archivo' >&2
  exit 1
fi

if [[ "$#" -eq 0 ]]; then
  echo "ERROR: indica al menos un archivo (o carpeta) a enviar." >&2
  echo "       Ejemplo: bash scripts/enviar_archivos.sh config/datos.json seed.sql" >&2
  exit 1
fi

# Archivos a enviar: todos los argumentos posicionales.
FILES=("$@")

# Valida que cada archivo exista antes de intentar copiar.
for f in "${FILES[@]}"; do
  if [[ ! -e "${f}" ]]; then
    echo "ERROR: no existe '${f}'." >&2
    exit 1
  fi
done

if [[ ! -f "${SSH_KEY}" ]]; then
  echo "ERROR: no se encontró la llave SSH en '${SSH_KEY}'." >&2
  echo "       Ajusta la variable de entorno SSH_KEY." >&2
  exit 1
fi

# shellcheck disable=SC2206
NODES_ARR=(${NODES})

SSH_OPTS=(-i "${SSH_KEY}" -o StrictHostKeyChecking=accept-new)

SCP_OPTS=("${SSH_OPTS[@]}")
if [[ "${RECURSIVE}" == "1" ]]; then
  SCP_OPTS+=(-r)
fi

for node in "${NODES_ARR[@]}"; do
  user="${node%@*}"
  dest_dir="/home/${user}/${DEST_SUBDIR}"

  echo "==> Enviando a ${node} -> ${dest_dir} ..."

  # Asegura que la carpeta destino exista.
  if ! ssh "${SSH_OPTS[@]}" "${node}" "mkdir -p '${dest_dir}'"; then
    echo "    AVISO: no se pudo crear ${dest_dir} en ${node}. Se omite." >&2
    continue
  fi

  # Copia cada archivo indicado.
  for f in "${FILES[@]}"; do
    if scp "${SCP_OPTS[@]}" "${f}" "${node}:${dest_dir}/"; then
      echo "    OK -> ${node}:${dest_dir}/$(basename "${f}")"
    else
      echo "    ERROR copiando '${f}' a ${node}." >&2
    fi
  done
done

echo "==> Envío finalizado."
