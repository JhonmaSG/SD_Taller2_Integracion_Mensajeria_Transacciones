# Dockerización y Despliegue Multi-VM con Docker Compose

Este documento describe la arquitectura, configuración y funcionamiento de la dockerización del sistema distribuido utilizando **Docker Compose con Profiles** y **Tailscale**.

---

## 1. Arquitectura de Contenedores

El despliegue está diseñado para ejecutarse en **3 Máquinas Virtuales (VMs) separadas**, interconectadas mediante una red privada Tailscale. En lugar de tener archivos compose independientes por máquina, se utiliza un único [`docker-compose.yml`](../docker-compose.yml) orquestado mediante **perfiles de Docker Compose** (`profiles: ["nodo1"]`, `["nodo2"]`, `["nodo3"]`).

```mermaid
flowchart TD
    subgraph VM1["VM 1 — Follower (100.103.251.117)"]
        M1[("sd-mongod\n:27017")]
        N1["sd-nodo (Bully ID 1)\n:5000 gRPC"]
        M1 <--> N1
    end

    subgraph VM2["VM 2 — Follower (100.123.154.67)"]
        M2[("sd-mongod\n:27017")]
        N2["sd-nodo (Bully ID 2)\n:5000 gRPC"]
        M2 <--> N2
    end

    subgraph VM3["VM 3 — Coordinador (100.69.45.6)"]
        M3[("sd-mongod\n:27017")]
        RMQ["sd-rabbitmq\n:5672 AMQP\n:15672 UI"]
        MI["sd-mongo-init\n(scripts/mongo-rs-init.sh)"]
        N3["sd-nodo (Bully ID 3)\n:5000 gRPC"]
        M3 <--> N3
        RMQ <--> N3
        MI -.->|rs.initiate| M3
    end

    %% Replicación MongoDB
    M1 <===>|Replicación rs-grupo| M3
    M2 <===>|Replicación rs-grupo| M3
    M1 <===>|Replicación rs-grupo| M2

    %% Comunicación gRPC entre nodos Bully
    N1 <--->|gRPC 5000| N2
    N1 <--->|gRPC 5000| N3
    N2 <--->|gRPC 5000| N3

    %% RabbitMQ connections
    N1 -.->|AMQP 5672| RMQ
    N2 -.->|AMQP 5672| RMQ
```

### Distribución de Servicios por Perfil

| Servicio en Compose | Imagen / Build | Perfil activo | Rol y Descripción |
|---|---|---|---|
| `mongod` | `mongo:9.0` | `nodo1`, `nodo2`, `nodo3` | Instancia local de MongoDB en cada VM, configurada con `--replSet rs-grupo --port 27017`. |
| `mongo-init` | `mongo:9.0` | `nodo3` | Tarea de inicialización única (`restart: "no"`). Ejecuta `scripts/mongo-rs-init.sh` para conectar los 3 nodos en el Replica Set. |
| `rabbitmq` | `rabbitmq:3.13-management` | `nodo3` | Broker AMQP centralizado en el coordinador (puertos `5672` y panel web `15672`). |
| `nodo1` | `src/bully-algorithm/Dockerfile` | `nodo1` | Nodo Go con ID=1 en VM1 (puerto gRPC `5000`). |
| `nodo2` | `src/bully-algorithm/Dockerfile` | `nodo2` | Nodo Go con ID=2 en VM2 (puerto gRPC `5000`). |
| `nodo3` | `src/bully-algorithm/Dockerfile` | `nodo3` | Nodo Go con ID=3 en VM3 (Coordinador inicial Bully, puerto gRPC `5000`). |

---

## 2. Red y Conectividad: `network_mode: host`

Todos los contenedores utilizan `network_mode: host`. Esto es fundamental por dos razones:

1. **Visibilidad directa en Tailscale:** Los puertos abiertos (`5000` gRPC, `27017` MongoDB, `5672`/`15672` RabbitMQ) quedan expuestos directamente en la interfaz `tailscale0` de cada VM. No hay capas de traducción NAT ni mapeos de puertos internos.
2. **Compatibilidad con pruebas de partición (`iptables`):** Los scripts de partición de red del taller (`scripts/particion/aislar.sh` y `reconectar.sh`) manipulan reglas de cortafuegos en la interfaz `tailscale0` bloqueando los puertos `5000` y `27017`. Al usar modo host, las reglas de iptables aíslan efectivamente a los contenedores sin necesidad de configurar redes puente (*bridge*).

---

## 3. Inicialización Automática del Replica Set (`scripts/mongo-rs-init.sh`)

En lugar de requerir que el usuario ingrese a la consola `mongosh` manualmente:
- El servicio `mongo-init` en la VM 3 monta el script [`scripts/mongo-rs-init.sh`](../scripts/mongo-rs-init.sh).
- El script:
  1. Espera a que el `mongod` local responda en el puerto 27017.
  2. Sondea a los otros dos miembros remotos vía Tailscale (`RS_MEMBER_1` y `RS_MEMBER_2`) hasta que estén listos.
  3. Ejecuta `rs.initiate()` asignando mayor prioridad (`priority: 2`) a la VM 3 para consolidar el primario.
  4. Es **idempotente**: si el Replica Set ya fue inicializado, detecta `rs.status().ok === 1` y no realiza cambios.

---

## 4. Script Wrapper de Despliegue: `scripts/levantar_vm.sh`

El script [`scripts/levantar_vm.sh`](../scripts/levantar_vm.sh) simplifica el despliegue soportando paso de parámetros o detección automática:

### Uso con parámetro (Recomendado):
```bash
# En la Máquina 1:
sudo bash scripts/levantar_vm.sh 1 --build -d

# En la Máquina 2:
sudo bash scripts/levantar_vm.sh 2 --build -d

# En la Máquina 3 (Coordinador + RabbitMQ):
sudo bash scripts/levantar_vm.sh 3 --build -d
```

### Uso sin parámetro:
Si no se pasa el número como argumento, el script lee automáticamente el campo `NODE_ID` del archivo `.env` local y ejecuta:
```bash
docker compose --profile nodo<NODE_ID> up [opciones adicionales]
```

---

## 5. Configuración de Variables de Entorno (`.env`)

Cada máquina virtual cuenta con su plantilla preconfigurada con las IPs reales:

- **VM 1:** [`.env.vm1`](../.env.vm1) (Copia a `.env` con `cp .env.vm1 .env`)
- **VM 2:** [`.env.vm2`](../.env.vm2) (Copia a `.env` con `cp .env.vm2 .env`)
- **VM 3:** [`.env.vm3`](../.env.vm3) (Copia a `.env` con `cp .env.vm3 .env`)

### Claves principales:
- `NODE_ID`: ID numérico del nodo (1, 2 o 3).
- `NODE_HOST`: IP Tailscale de esta VM (dirección anunciada al cluster).
- `NODE_PORT`: `5000` (puerto gRPC).
- `PEERS`: Lista de los otros nodos en formato `<id>@<ip>:<puerto>`.
- `DB_URI`: Cadena de conexión Replica Set: `mongodb://100.103.251.117:27017,100.123.154.67:27017,100.69.45.6:27017/?replicaSet=rs-grupo&w=majority&wtimeoutMS=5000&retryWrites=true`.
- `RABBITMQ_URL`: En VM 1 y VM 2 apunta a la IP de VM 3 (`amqp://sd_user:cambiar_esta_clave@100.69.45.6:5672/`). En VM 3 apunta a `localhost`.

---

## 6. Comandos de Operación y Monitoreo

### Ver estado de los contenedores
```bash
sudo docker compose ps
# o con Docker nativo:
sudo docker ps
```

### Ver logs del nodo Go
```bash
sudo docker logs -f sd-nodo
```

### Consultar el estado en MongoDB (desde cualquier VM)
```bash
sudo docker exec -it sd-mongod mongosh inventory --quiet --eval "db.nodes.find().pretty()"
```

### Consultar salud del Replica Set
```bash
sudo docker exec -it sd-mongod mongosh --quiet --eval \
  "rs.status().members.map(m => m.name + ' -> ' + m.stateStr)"
```

### Detener los servicios
```bash
# Detener sin borrar volúmenes:
sudo docker compose down

# Detener y reiniciar base de datos limpia (borrando volúmenes):
sudo docker compose down -v
```

---

## 7. Pruebas de Tolerancia a Fallos y Partición de Red

### Prueba A: Caída del Coordinador (Failover de Bully)
1. En la Máquina 3, detener el nodo:
   ```bash
   sudo docker stop sd-nodo
   ```
2. En la Máquina 2, observar logs:
   ```bash
   sudo docker logs -f sd-nodo
   ```
   *El Nodo 2 detecta la ausencia de heartbeats en 3 segundos (`TIMEOUT_MS`), inicia elección Bully y se proclama nuevo `COORDINATOR`.*
3. En MongoDB, verificar que `active_coordinator` cambia a `"node_2"`.
4. Reconectar el Nodo 3:
   ```bash
   sudo docker start sd-nodo
   ```
   *El Nodo 3 recupera el rol de coordinador pacíficamente al tener mayor prioridad.*

### Prueba B: Aislamiento por Partición de Red (Scripts iptables)
1. En el nodo a aislar (ej. Máquina 1):
   ```bash
   sudo ./scripts/particion/aislar.sh --interface tailscale0 --grpc-port 5000 --mongo-port 27017
   ```
2. En la Máquina 3, verificar que el detector de fallos marca al nodo como `OFFLINE` en MongoDB tras superar `TIMEOUT_MS`.
3. Restaurar la conectividad:
   ```bash
   sudo ./scripts/particion/reconectar.sh
   ```
   *El nodo restablece conexión y vuelve automáticamente a estar `ONLINE`.*
