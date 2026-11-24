# Pruebas de desarrollo

> ⚠️ Estos scripts son **solo para pruebas de desarrollo**. No forman parte del despliegue productivo; sirven para construir la imagen del nodo y moverla manualmente entre máquinas de la tailnet mientras se prueba.

## `scripts/generar_imagen.sh`

Construye la imagen Docker del nodo y la exporta a un `.tar` con `docker save`.

### Opciones (variables de entorno)

- `IMAGE_NAME`: nombre:tag de la imagen. Por defecto `bully-nodo:latest`.
- `OUTPUT`: ruta del `.tar` de salida. Por defecto `<raiz>/.runtime/images/bully-nodo.tar`.
- `MODULE_DIR`: ruta del módulo con el Dockerfile. Por defecto `<raiz>/src/bully-algorithm`.
- `PLATFORM`: plataforma de build. Por defecto `linux/amd64`.

### Paso a paso

1. Genera la imagen con valores por defecto:

   ```bash
   bash scripts/generar_imagen.sh
   ```

2. (Opcional) genera con valores personalizados:

   ```bash
   IMAGE_NAME=bully-nodo:v1 OUTPUT=/tmp/bully.tar bash scripts/generar_imagen.sh
   ```

3. Envía el `.tar` a los nodos con `enviar_archivos.sh`.
4. En cada nodo, carga la imagen:

   ```bash
   docker load -i /home/<usuario>/test/bully-nodo.tar
   ```

## `scripts/enviar_archivos.sh`

Copia uno o varios archivos o carpetas a cada máquina remota vía `scp` (SSH).

### Opciones (variables de entorno)

- `NODES` (obligatoria): nodos `usuario@host` separados por espacios.
- `DEST_SUBDIR`: subcarpeta destino dentro del HOME. Por defecto `test` (destino: `/home/<usuario>/<DEST_SUBDIR>`).
- `SSH_KEY`: ruta a la llave privada. Por defecto `~/.ssh/id_ed25519_vm`.
- `RECURSIVE`: `1` para copiar carpetas recursivamente (`scp -r`).

### Paso a paso

1. Define `NODES` y ejecuta indicando uno o más archivos:

   ```bash
   NODES="nodo1@100.x.x.1 nodo2@100.x.x.2" \
   bash scripts/enviar_archivos.sh archivo1 [archivo2 ...]
   ```

2. (Opcional) cambia la carpeta destino:

   ```bash
   NODES="nodo1@100.0.0.1" DEST_SUBDIR=bully \
   bash scripts/enviar_archivos.sh seed.sql .env.example
   ```

3. (Opcional) copia una carpeta completa:

   ```bash
   NODES="nodo1@100.0.0.1" RECURSIVE=1 \
   bash scripts/enviar_archivos.sh docs/
   ```

4. Verifica la salida: cada copia exitosa muestra `OK -> ...` y al final aparece `==> Envío finalizado.`.
