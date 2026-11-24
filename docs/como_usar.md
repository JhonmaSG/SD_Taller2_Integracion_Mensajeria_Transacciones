# Cómo usar los scripts

## `scripts/preparar-nodo.sh`

### 1. Qué hace y para qué sirve

Instala Docker Engine y Docker Compose en Ubuntu Server 26.04 LTS.

El script:

- Comprueba que se ejecute como `root`.
- Intenta eliminar paquetes en conflicto: `docker.io`, `docker-doc`, `docker-compose`, `docker-compose-v2`, `podman-docker`, `containerd` y `runc`.
- Instala `ca-certificates` y `curl`.
- Agrega la llave GPG y el repositorio de Docker.
- Instala `docker-ce`, `docker-ce-cli`, `containerd.io`, `docker-buildx-plugin` y `docker-compose-plugin`.
- Configura el límite de logs en `/etc/docker/daemon.json` si ese archivo no existe.
- Habilita, inicia y reinicia el servicio Docker.
- Ejecuta comprobaciones de Docker y Compose.

### 2. Requisitos previos

- Ejecutar el script como `root`. Si no se ejecuta como `root`, el script indica usar `sudo` y termina.
- Contar con `apt-get`, `dpkg`, `systemctl` y el archivo `/etc/os-release`.
- Tener acceso para descargar la llave GPG y consultar el repositorio de Docker en `https://download.docker.com`.
- El sistema debe proporcionar `UBUNTU_CODENAME` o `VERSION_CODENAME` mediante `/etc/os-release`.

### 3. Pasos de ejecución en orden

**Inicio de la ejecución:** ejecuta únicamente el script con:

```bash
sudo bash scripts/preparar-nodo.sh
```

Al ejecutar ese comando, el script realiza automáticamente los siguientes pasos, en este orden:

1. Comprueba que `EUID` sea `0`.
2. Intenta eliminar los paquetes en conflicto con `apt-get remove -y`.
3. Actualiza los índices de paquetes con `apt-get update`.
4. Instala las dependencias con `apt-get install -y ca-certificates curl`.
5. Crea `/etc/apt/keyrings`, descarga la llave GPG desde `https://download.docker.com/linux/ubuntu/gpg` en `/etc/apt/keyrings/docker.asc` y ejecuta `chmod a+r /etc/apt/keyrings/docker.asc`.
6. Carga `/etc/os-release` y obtiene el valor de `CODENAME` desde `UBUNTU_CODENAME` o `VERSION_CODENAME`.
7. Comprueba el repositorio en `https://download.docker.com/linux/ubuntu/dists/${CODENAME}/Release`. Si la comprobación falla, cambia `CODENAME` a `noble`.
8. Crea `/etc/apt/sources.list.d/docker.sources` con el repositorio estable de Docker y la arquitectura obtenida con `dpkg --print-architecture`.
9. Actualiza de nuevo los índices de paquetes con `apt-get update`.
10. Instala Docker, containerd, Buildx y Compose con `apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin`.
11. Crea `/etc/docker` y, si no existe `/etc/docker/daemon.json`, lo configura con `json-file`, `max-size` `10m` y `max-file` `3`.
12. Habilita e inicia Docker con `systemctl enable --now docker`.
13. Reinicia Docker con `systemctl restart docker`.
14. Ejecuta las comprobaciones `docker --version`, `docker compose version` y `docker run --rm hello-world`.

### 4. Cómo verificar que funcionó

- `docker --version` debe ejecutarse como parte de la verificación.
- `docker compose version` debe ejecutarse como parte de la verificación.
- `docker run --rm hello-world` debe ejecutarse como parte de la verificación.
- Si las comprobaciones terminan correctamente, el script muestra:

  ```text
  Listo.
  ```

## `scripts/levantar_vm.sh` y `docker-compose.yml`

### 1. Qué hace y para qué sirve
Permite levantar el stack de servicios de cada Máquina Virtual con Docker Compose usando perfiles (`nodo1`, `nodo2`, `nodo3`).

- En **VM 1 (`nodo1`)**: Levanta `sd-mongod` y `sd-nodo` (ID 1).
- En **VM 2 (`nodo2`)**: Levanta `sd-mongod` y `sd-nodo` (ID 2).
- En **VM 3 (`nodo3` - Coordinador)**: Levanta `sd-mongod`, `sd-rabbitmq`, `sd-mongo-init` y `sd-nodo` (ID 3).
- Utiliza `network_mode: host` para comunicarse sin sobrecarga de NAT sobre la red Tailscale.

### 2. Formas de ejecución
```bash
# Con número de nodo explícito:
sudo bash scripts/levantar_vm.sh 1 --build -d
sudo bash scripts/levantar_vm.sh 2 --build -d
sudo bash scripts/levantar_vm.sh 3 --build -d

# Sin argumento (detecta NODE_ID del archivo .env local):
sudo bash scripts/levantar_vm.sh --build -d
```

### 3. Cómo verificar que funcionó
- `sudo docker ps`: Los contenedores deben estar en estado `Up` o `Up (healthy)`.
- `sudo docker logs -f sd-nodo`: El nodo Go debe mostrar conexión exitosa a MongoDB y participar en el algoritmo Bully.
- Consulta en MongoDB (desde VM 3):
  ```bash
  sudo docker exec -it sd-mongod mongosh inventory --quiet --eval "db.nodes.find().pretty()"
  ```
  Los tres nodos deben figurar con `status: 'ONLINE'`.

