# Guía Paso a Paso: Acceso SSH y Configuración de Tailscale

Esta guía explica detalladamente cómo conectar tu máquina anfitriona (tu PC con Windows/Mac/Linux) con las máquinas virtuales (VMs) mediante **Tailscale** y **SSH**, permitiéndote **copiar y pegar comandos directamente desde tu terminal** sin tener que escribirlos manualmente en la ventana del hipervisor (VirtualBox, VMware, etc.).

---

## Índice
1. [¿Por qué usar SSH y Tailscale?](#1-por-qué-usar-ssh-y-tailscale)
2. [Paso 1: Configurar Tailscale en tu PC anfitriona](#paso-1-configurar-tailscale-en-tu-pc-anfitriona)
3. [Paso 2: Instalar y conectar Tailscale en cada VM](#paso-2-instalar-y-conectar-tailscale-en-cada-vm)
4. [Paso 3: Configurar SSH por clave pública en las VMs](#paso-3-configurar-ssh-por-clave-pública-en-las-vms)
5. [Paso 4: Conectarse y copiar/pegar comandos](#paso-4-conectarse-y-copiarpegar-comandos)
6. [Paso 5: Configurar atajos rápidos con `~/.ssh/config`](#paso-5-configurar-atajos-rápidos-con-sshconfig)
7. [Solución de problemas comunes](#solución-de-problemas-comunes)

---

## 1. ¿Por qué usar SSH y Tailscale?

- **Consola del hipervisor (VirtualBox/VMware):** No suele permitir copiar y pegar texto largo o bloques de comandos fácilmente.
- **Tailscale:** Crea una red privada virtual (VPN Mesh) segura entre tu PC y las VMs. No necesitas configurar redirección de puertos (*port forwarding*) ni lidiar con IPs locales que cambian. Todas las máquinas reciben una IP fija del rango `100.x.x.x`.
- **SSH (Secure Shell):** Te permite abrir pestañas en tu terminal favorita de la máquina anfitriona (PowerShell, Windows Terminal, iTerm2, etc.) y tener control total con `Ctrl+V` / `Ctrl+Shift+V`.

---

## 2. Paso 1: Configurar Tailscale en tu PC anfitriona

1. **Crear cuenta en Tailscale:**
   - Entra a [tailscale.com](https://tailscale.com/) e inicia sesión (con Google, GitHub, Microsoft, etc.).
2. **Instalar el cliente en tu PC:**
   - **Windows:** Descarga e instala [Tailscale para Windows](https://tailscale.com/download/windows).
   - **macOS:** Instala desde la Mac App Store o con `brew install --cask tailscale`.
   - **Linux:** `curl -fsSL https://tailscale.com/install.sh | sh`
3. **Iniciar sesión en el cliente:**
   - Abre la aplicación de Tailscale en tu PC y haz clic en **Log in**.
   - Completa el inicio de sesión en tu navegador con la misma cuenta.
   - En el panel de control de Tailscale (*Admin Console*), verás aparecer tu PC anfitrión.

---

## 3. Paso 2: Instalar y conectar Tailscale en cada VM

En cada una de las 3 máquinas virtuales (en la ventana de la VM):

1. **Iniciar sesión con tu usuario de Ubuntu.**
2. **Instalar Tailscale:**
   ```bash
   curl -fsSL https://tailscale.com/install.sh | sh
   ```
3. **Conectar la VM a tu red de Tailscale:**
   ```bash
   sudo tailscale up
   ```
   - La terminal te mostrará un enlace similar a:
     ```text
     To authenticate, visit:
             https://login.tailscale.com/a/0123456789ab
     ```
   - Abre ese enlace en el navegador de tu PC anfitriona (donde ya tienes tu sesión de Tailscale abierta) y pulsa **Connect / Authorize**.
4. **Obtener la IP Tailscale asignada a la VM:**
   ```bash
   tailscale ip -4
   ```
   - Te devolverá una dirección IP que empieza por `100.` (ejemplo: `100.85.120.45`).
   - **Anota esta IP para cada VM.**

---

## 4. Paso 3: Configurar SSH por clave pública en las VMs

Para máxima seguridad y comodidad (sin escribir contraseñas cada vez), configuramos SSH por par de llaves criptográficas (Ed25519).

### 4.1 Generar la llave SSH en tu PC anfitriona (solo se hace una vez)
Abre **PowerShell** o tu terminal local y ejecuta:
```powershell
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519_vm
```
*(Presiona Enter dos veces para no ponerle contraseña a la llave, o ponle una frase de paso si lo prefieres).*

Esto generará dos archivos en tu carpeta personal:
- `~/.ssh/id_ed25519_vm` (clave privada — nunca la compartas)
- `~/.ssh/id_ed25519_vm.pub` (clave pública — la que pondremos en las VMs)

### 4.2 Copiar la clave pública a cada VM

#### Método A: Si tienes `ssh-copy-id` (Linux/Mac o Git Bash en Windows)
Ejecuta desde tu PC anfitriona para cada VM (usando su IP de Tailscale):
```bash
ssh-copy-id -i ~/.ssh/id_ed25519_vm.pub usuario@100.x.x.1
ssh-copy-id -i ~/.ssh/id_ed25519_vm.pub usuario@100.x.x.2
ssh-copy-id -i ~/.ssh/id_ed25519_vm.pub usuario@100.x.x.3
```
*(Te pedirá la contraseña del usuario de la VM una última vez).*

#### Método B: Manualmente desde PowerShell en Windows
Si en Windows PowerShell no tienes el comando `ssh-copy-id`:
```powershell
# Ver el contenido de tu clave pública:
Get-Content ~/.ssh/id_ed25519_vm.pub
```
Copia esa línea de texto (empieza por `ssh-ed25519 AAAA...`).

Luego, en la consola de cada VM:
```bash
mkdir -p ~/.ssh
chmod 700 ~/.ssh
nano ~/.ssh/authorized_keys
```
Pega la clave pública al final del archivo, guarda con `Ctrl+O`, `Enter` y sal con `Ctrl+X`.
Ajusta los permisos:
```bash
chmod 600 ~/.ssh/authorized_keys
```

### 4.3 Deshabilitar autenticación por contraseña (Requerimiento del taller)
Una vez que verifiques que entras con la clave, en cada VM edita la configuración del servicio SSH:
```bash
sudo nano /etc/ssh/sshd_config.d/deshabilitar_password.conf
```
Agrega la siguiente línea:
```text
PasswordAuthentication no
```
Guarda y reinicia el servicio SSH:
```bash
sudo systemctl restart ssh
```

---

## 5. Paso 4: Conectarse y copiar/pegar comandos

Ahora puedes minimizar las ventanas de VirtualBox/VMware. Abre **Windows Terminal** o **PowerShell** en tu máquina física:

```powershell
ssh -i ~/.ssh/id_ed25519_vm usuario@100.x.x.1
```

¡Listo! Ya estás dentro de la VM. En esta ventana puedes:
- **Copiar comandos:** Selecciónalos de esta guía con `Ctrl+C`.
- **Pegar comandos:** En Windows Terminal / PowerShell, haz clic derecho o presiona `Ctrl+V` (o `Ctrl+Shift+V` según tu terminal).

---

## 6. Paso 5: Configurar atajos rápidos con `~/.ssh/config`

Para no tener que escribir la IP ni la ruta de la llave cada vez, puedes crear un archivo de configuración en tu PC.

En tu PC anfitriona, edita o crea el archivo `~/.ssh/config` (en Windows: `C:\Users\<TuUsuario>\.ssh\config`):

```ssh-config
Host vm1
    HostName 100.x.x.1
    User usuario
    IdentityFile ~/.ssh/id_ed25519_vm

Host vm2
    HostName 100.x.x.2
    User usuario
    IdentityFile ~/.ssh/id_ed25519_vm

Host vm3
    HostName 100.x.x.3
    User usuario
    IdentityFile ~/.ssh/id_ed25519_vm
```
*(Reemplaza `100.x.x.N` por las IPs reales de Tailscale y `usuario` por tu usuario en Ubuntu).*

Ahora puedes conectarte simplemente escribiendo:
```powershell
ssh vm1
```
o para las otras máquinas:
```powershell
ssh vm2
ssh vm3
```

Puedes abrir 3 pestañas en tu Windows Terminal, una conectada a cada VM (`vm1`, `vm2`, `vm3`), y ejecutar y monitorear todo en paralelo.

---

## Solución de problemas comunes

### 1. "Permission denied (publickey)"
- Asegúrate de indicar la llave correcta: `ssh -i ~/.ssh/id_ed25519_vm usuario@IP`.
- Verifica los permisos en la VM:
  ```bash
  chmod 700 ~/.ssh
  chmod 600 ~/.ssh/authorized_keys
  ```
- Comprueba que el usuario indicado (`usuario@...`) coincida exactamente con el usuario de Ubuntu.

### 2. "Connection timed out" o "No route to host"
- Comprueba que Tailscale esté activo en tu PC (icono en la barra de tareas en verde).
- En la VM, ejecuta `sudo tailscale status` para confirmar que esté conectada.
- Verifica con un ping desde tu PC: `ping 100.x.x.1`.

### 3. "WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!"
Ocurre si reinstalaste la VM y cambió su clave interna. En tu PC anfitriona ejecuta:
```powershell
ssh-keygen -R 100.x.x.1
```
y vuelve a intentar la conexión.
