#!/usr/bin/env bash
# Instalación de Docker Engine + Compose en Ubuntu Server 26.04 LTS
# Uso: sudo bash install-docker.sh
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "Ejecuta con sudo: sudo bash $0" >&2
  exit 1
fi

echo "==> Eliminando paquetes en conflicto (si existen)..."
for pkg in docker.io docker-doc docker-compose docker-compose-v2 podman-docker containerd runc; do
  apt-get remove -y "$pkg" >/dev/null 2>&1 || true
done

echo "==> Instalando dependencias..."
apt-get update
apt-get install -y ca-certificates curl

echo "==> Agregando llave GPG de Docker..."
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc

# Codename de 26.04 (resolute). Si Docker aún no publica ese repo,
# se usa "noble" (24.04) como respaldo; es compatible.
. /etc/os-release
CODENAME="${UBUNTU_CODENAME:-$VERSION_CODENAME}"
if ! curl -fsI "https://download.docker.com/linux/ubuntu/dists/${CODENAME}/Release" >/dev/null; then
  echo "AVISO: Docker no tiene repo para '${CODENAME}' todavía. Usando 'noble'."
  CODENAME="noble"
fi

echo "==> Agregando repositorio (${CODENAME})..."
cat > /etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: ${CODENAME}
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF

echo "==> Instalando Docker..."
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

echo "==> Configurando límite de logs..."
mkdir -p /etc/docker
if [[ ! -f /etc/docker/daemon.json ]]; then
  cat > /etc/docker/daemon.json <<'EOF'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" }
}
EOF
fi

systemctl enable --now docker
systemctl restart docker

echo "==> Verificando..."
docker --version
docker compose version
docker run --rm hello-world

echo "Listo."
