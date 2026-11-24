# Sistemas Distribuidos Modernos — Talleres 1 y 2

Ingeniería en Telemática · Universidad Distrital Francisco José de Caldas
Docente: Msc. Ing. Óscar Gabriel Espejo Mojica

## 1. Descripción

**Taller 1 — Coordinación y datos del sistema:** elección de coordinador
(Bully), inventario replicado (CPU, memoria, ancho de banda), detección de
conflictos con vectores de versión y experimento de partición de red.

**Taller 2 — Integración, mensajería y transacciones:** comunicación gRPC
entre agentes y coordinador, mensajería asíncrona con RabbitMQ, orden causal
con relojes de Lamport y asignación transaccional con patrón Saga.

## 2. Arquitectura

| Nodo  | Rol                                             | IP Tailscale   | RAM  |
| ----- | ----------------------------------------------- | -------------- | ---- |
| nodo1 | Agente / posible coordinador                    | 100.88.227.26  | 2 GB |
| nodo2 | Agente / posible coordinador                    | 100.70.53.0    | 2 GB |
| nodo3 | Agente / Coordinador / RabbitMQ / MongoDB       | 100.114.102.71 | 3 GB |

Cada nodo ejecuta un proceso Go con: servicio gRPC de elección y heartbeats,
servicio gRPC del agente (Reserve, Release, NotifyAllocation), publicador y
consumidor RabbitMQ, reloj de Lamport, persistencia en MongoDB y orquestador
de Saga.

**Colas RabbitMQ:** `metrics`, `allocations`, `failover`.

## 3. Requisitos

- Ubuntu Server 26.04.1 LTS o Debian 13 (sin GUI)
- Tailscale (red privada entre las VMs)
- Docker Engine y Docker Compose plugin v2
- Acceso SSH por clave pública

## 4. Instalación y ejecución

La guía completa de dockerización está en [`docs/docker.md`](docs/docker.md).

### 4.1 Preparar configuración por VM

    # VM 1:  cp .env.vm1 .env
    # VM 2:  cp .env.vm2 .env
    # VM 3 (Coordinador / broker / BD):  cp .env.vm3 .env

Variables principales: `[RABBITMQ_URL, MONGO_URI, NODE_ID, MANUAL_METRICS]`.

| Variable | Descripción | Valor por defecto |
|---|---|---|
| `RABBITMQ_URL` | URL del broker | `[amqp://...@100.114.102.71:5672/]` |
| `MONGO_URI` | URI de MongoDB | `[mongodb://100.114.102.71:27017]` |
| `NODE_ID` | Identificador del nodo | `[node-1 / node-2 / node-3]` |
| `MANUAL_METRICS` | `true` desactiva las métricas periódicas (para pruebas) | `false` |

### 4.2 Levantar los servicios

    sudo bash scripts/levantar_vm.sh 1 --build -d   # nodo 1
    sudo bash scripts/levantar_vm.sh 2 --build -d   # nodo 2
    sudo bash scripts/levantar_vm.sh 3 --build -d   # nodo 3 (+ RabbitMQ + MongoDB)

> **Estado de Docker Compose (Taller 2):** el `docker-compose.yml` integrado
> de todos los componentes está pendiente de integración desde otra rama.

### 4.3 Documentación de apoyo

- [`docs/docker.md`](docs/docker.md) · Guía de Docker
- [`docs/guia_ssh_tailscale.md`](docs/guia_ssh_tailscale.md) · SSH y Tailscale
- [`docs/comandos.md`](docs/comandos.md) · Comandos del taller
- [`docs/como_usar.md`](docs/como_usar.md) · Uso de scripts

## 5. Uso de la API y pruebas

### 5.1 Reportar una métrica
    curl -X POST http://[IP:PUERTO]/[ruta-metricas] -H "Content-Type: application/json" -d '[JSON]'

### 5.2 Solicitar una asignación (inicia la Saga)
    curl -X POST http://[IP:PUERTO]/[ruta-asignacion] -H "Content-Type: application/json" -d '[JSON]'

### 5.3 Pruebas del Taller 2
- **E3 — Flujo completo:** POST de asignación y verificación de la Saga en `COMPLETED`.
- **E4 — Falla en el paso 2:** forzar el error con `collMod` sobre `allocations` y verificar `COMPENSATED`.
- **E5 — Desconexión del broker:** detener y restablecer el nodo 3 y verificar la reconexión.
- **Orden de Lamport:** con `MANUAL_METRICS=true`, publicar eventos concurrentes desde dos nodos.

### 5.4 Pruebas del Taller 1
    sudo docker exec -it sd-mongod mongosh inventory --quiet --eval "db.nodes.find().pretty()"
    sudo ./scripts/particion/aislar.sh --interface tailscale0 --grpc-port 5000 --mongo-port 27017
    sudo ./scripts/particion/reconectar.sh

## 6. Decisiones técnicas

- Algoritmo de elección: `docs/decisiones/ADR-001-algoritmo-eleccion.md`
- Base de datos: `docs/decisiones/ADR-002-base-de-datos.md`
- Regla de conflictos: `docs/decisiones/ADR-003-regla-conflictos.md`
- Mensajería y Saga: `[ADR-004 si lo crean]`

## 7. Bibliotecas de terceros

| Biblioteca | Versión | Uso | Licencia |
| ---------- | ------- | --- | -------- |
| google.golang.org/grpc | v1.76.0 | Comunicación síncrona entre nodos | Apache-2.0 |
| google.golang.org/protobuf | v1.36.6 | Contratos `.proto` | BSD-3-Clause |
| go.mongodb.org/mongo-driver/v2 | v2.4.0 | Persistencia en MongoDB | Apache-2.0 |
| github.com/rabbitmq/amqp091-go | v1.17.0 | Cliente AMQP para RabbitMQ | BSD-2-Clause |

Dependencias indirectas (compresión, autenticación SCRAM y utilidades
`golang.org/x/*`): ver `go.mod`.

## 8. Normas cumplidas

Código propio; bibliotecas de terceros citadas arriba; sin servicios en la
nube de pago ni VPS; sin claves privadas en el repositorio; solo máquinas del
grupo conectadas por Tailscale.
