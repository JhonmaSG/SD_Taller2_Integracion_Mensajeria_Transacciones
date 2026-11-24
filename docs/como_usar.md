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

## `scripts/enviar_archivos.sh`

### 1. Qué hace y para qué sirve

Copia uno o varios archivos o carpetas a cada máquina remota mediante `scp` y `SSH`.

El script recibe los archivos o carpetas como argumentos, crea en cada nodo el destino `/home/<usuario>/<DEST_SUBDIR>` mediante `mkdir -p` y copia allí cada elemento indicado. No compila; solo transfiere los archivos que se indiquen.

### 2. Requisitos previos

- Definir la variable de entorno obligatoria `NODES` con los nodos en formato `usuario@host`, separados por espacios.
- Indicar al menos un archivo o carpeta como argumento del script.
- Cada archivo o carpeta indicado debe existir.
- Contar con una llave privada SSH. Por defecto, el script usa `$HOME/.ssh/id_ed25519_vm`; se puede cambiar con la variable `SSH_KEY`.
- Tener acceso SSH por llave a cada nodo, con la contraseña deshabilitada.
- Para copiar carpetas recursivamente, definir `RECURSIVE=1`.
- `DEST_SUBDIR` es opcional y por defecto tiene el valor `test`.

### 3. Pasos de ejecución en orden

**Inicio de la ejecución:** define `NODES` y ejecuta el script indicando uno o varios archivos o carpetas:

```bash
NODES="nodo1@100.x.x.1 nodo2@100.x.x.2" \
bash scripts/enviar_archivos.sh archivo1 [archivo2 ...]
```

También puedes definir `DEST_SUBDIR`, `SSH_KEY` o `RECURSIVE` en el mismo comando.

Al ejecutar ese comando, el script realiza automáticamente los siguientes pasos, en este orden:

1. Comprueba que `NODES` no esté vacío.
2. Comprueba que se haya indicado al menos un archivo o carpeta.
3. Comprueba que cada archivo o carpeta indicado exista.
4. Comprueba que exista la llave definida por `SSH_KEY`.
5. Usa la llave SSH y `StrictHostKeyChecking=accept-new` para preparar las opciones de `ssh` y `scp`.
6. Si `RECURSIVE` vale `1`, agrega `-r` a las opciones de `scp`.
7. Para cada nodo, obtiene el usuario y construye el destino `/home/${user}/${DEST_SUBDIR}`.
8. Para cada nodo, ejecuta `mkdir -p` mediante `ssh` para asegurar que exista la carpeta destino.
9. Para cada archivo o carpeta indicado, ejecuta `scp` hacia `${node}:${dest_dir}/`.
10. Repite la creación del destino y la copia para todos los nodos de `NODES`.

También se pueden usar los ejemplos incluidos en el script:

```bash
NODES="nodo1@100.0.0.1 nodo2@100.0.0.2" \
bash scripts/enviar_archivos.sh config/datos.json
```

```bash
NODES="nodo1@100.0.0.1" DEST_SUBDIR=bully \
bash scripts/enviar_archivos.sh seed.sql .env.example
```

```bash
NODES="nodo1@100.0.0.1" RECURSIVE=1 \
bash scripts/enviar_archivos.sh docs/
```

### 4. Cómo verificar que funcionó

- Para cada copia exitosa, el script muestra una línea con este formato:

  ```text
  OK -> ${node}:${dest_dir}/$(basename "${f}")
  ```

- Si no puede crear la carpeta destino en un nodo, muestra un aviso con `no se pudo crear` y omite ese nodo.
- Si falla la copia de un archivo o carpeta, muestra un error con `ERROR copiando`.
- Al terminar el procesamiento de los nodos y archivos, muestra:

  ```text
  ==> Envío finalizado.
  ```

- El mensaje `Envío finalizado` no confirma que todas las copias hayan sido exitosas; las líneas `OK`, `AVISO` y `ERROR` indican el resultado de cada nodo y cada archivo o carpeta.

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

