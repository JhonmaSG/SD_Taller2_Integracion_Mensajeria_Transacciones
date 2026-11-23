# Taller 1 — Coordinación y datos del sistema

Sistemas Distribuidos Modernos · Ingeniería en Telemática

## 1. Descripción

Elección de coordinador, inventario replicado (CPU, memoria, ancho de banda), detección de conflictos con vectores de versión y experimento de partición de red.

## 2. Arquitectura

| Nodo  | Rol                                             | IP Tailscale | RAM  |
| ----- | ----------------------------------------------- | ------------ | ---- |
| nodo1 | Follower / Agente                               | 100.x.x.x    | 2 GB |
| nodo2 | Follower / Agente                               | 100.x.x.x    | 2 GB |
| nodo3 | Coordinador Bully / RabbitMQ / MongoDB Primario | 100.x.x.x    | 3 GB |

## 3. Requisitos

- Ubuntu Server 24.04 o 26.04 LTS (sin GUI)
- Tailscale (para interconexión de red privada entre las VMs)
- Docker Engine y Docker Compose plugin v2
- Acceso SSH por clave pública o contraseña

## 4. Instalación y ejecución con Docker Compose

  La guía completa de dockerización y arquitectura de contenedores se encuentra en [`docs/docker.md`](docs/docker.md).

### 4.1 Preparar configuración por VM

En cada máquina virtual, copia la plantilla `.env` correspondiente:

```bash
# En VM 1:
cp .env.vm1 .env

# En VM 2:
cp .env.vm2 .env

# En VM 3 (Coordinador):
cp .env.vm3 .env
```


### 4.2 Levantar los servicios

Ejecuta el script de arranque indicando el ID del nodo:

```bash
# En Máquina 1 (Follower):
sudo bash scripts/levantar_vm.sh 1 --build -d

# En Máquina 2 (Follower):
sudo bash scripts/levantar_vm.sh 2 --build -d

# En Máquina 3 (Coordinador + RabbitMQ + MongoDB Init):
sudo bash scripts/levantar_vm.sh 3 --build -d
```

### 4.3 Documentación de apoyo

- **Guía completa de Docker:** [`docs/docker.md`](docs/docker.md)
- **SSH y Tailscale (copiar y pegar comandos desde tu PC):** [`docs/guia_ssh_tailscale.md`](docs/guia_ssh_tailscale.md)
- **Comandos del taller:** [`docs/comandos.md`](docs/comandos.md)
- **Uso de scripts:** [`docs/como_usar.md`](docs/como_usar.md)

## 5. Pruebas y experimentos

```bash
# Consultar inventario y estado en MongoDB:
sudo docker exec -it sd-mongod mongosh inventory --quiet --eval "db.nodes.find().pretty()"

# Experimento de partición de red (aislar / reconectar):
sudo ./scripts/particion/aislar.sh --interface tailscale0 --grpc-port 5000 --mongo-port 27017
sudo ./scripts/particion/reconectar.sh
```

## 6. Decisiones técnicas

- Algoritmo de elección: `docs/decisiones/ADR-001-algoritmo-eleccion.md`
- Base de datos: `docs/decisiones/ADR-002-base-de-datos.md`
- Regla de conflictos: `docs/decisiones/ADR-003-regla-conflictos.md`

## 7. Bibliotecas de terceros

| Biblioteca | Versión | Uso | Licencia |
| ---------- | ------- | --- | -------- |
| TODO       |         |     |          |
