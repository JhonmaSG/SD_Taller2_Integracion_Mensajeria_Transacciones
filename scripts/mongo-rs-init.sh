#!/usr/bin/env bash
# =============================================================
# mongo-rs-init.sh
# Inicializa el Replica Set rs-grupo con las IPs Tailscale reales.
# Corre UNA SOLA VEZ en VM3 (coordinador) a través del servicio
# mongo-init del docker-compose.
#
# Variables requeridas (definidas en .env):
#   RS_MEMBER_1=100.x.x.1:27017
#   RS_MEMBER_2=100.x.x.2:27017
#   RS_MEMBER_3=100.x.x.3:27017   ← esta VM (coordinador, priority=2)
# =============================================================
set -Eeuo pipefail

RS_MEMBER_1="${RS_MEMBER_1:?Variable RS_MEMBER_1 no definida. Ejemplo: 100.x.x.1:27017}"
RS_MEMBER_2="${RS_MEMBER_2:?Variable RS_MEMBER_2 no definida. Ejemplo: 100.x.x.2:27017}"
RS_MEMBER_3="${RS_MEMBER_3:?Variable RS_MEMBER_3 no definida. Ejemplo: 100.x.x.3:27017}"

# ── 1. Esperar mongod local ──────────────────────────────────
echo "==> Esperando mongod local (localhost:27017)..."
until mongosh --host localhost:27017 --quiet \
      --eval "db.adminCommand('ping').ok" 2>/dev/null | grep -q 1; do
  echo "   ... mongod no responde aún, reintentando en 3s"
  sleep 3
done
echo "    mongod local OK"

# ── 2. Esperar los otros dos mongod via Tailscale ────────────
for MEMBER in "${RS_MEMBER_1}" "${RS_MEMBER_2}"; do
  HOST="${MEMBER%%:*}"
  PORT="${MEMBER##*:}"
  echo "==> Esperando mongod remoto ${MEMBER}..."
  for i in $(seq 1 30); do
    if mongosh --host "${HOST}:${PORT}" --quiet \
         --eval "db.adminCommand('ping').ok" 2>/dev/null | grep -q 1; then
      echo "    ${MEMBER} OK"
      break
    fi
    if [ "${i}" -eq 30 ]; then
      echo "ADVERTENCIA: ${MEMBER} no respondió tras 90s. Continuando de todas formas."
    fi
    echo "   ... intento ${i}/30, esperando 3s"
    sleep 3
  done
done

# ── 3. Comprobar si el RS ya está inicializado ───────────────
echo "==> Comprobando estado del Replica Set..."
RS_STATUS=$(mongosh --host localhost:27017 --quiet --eval "
  try { var s = rs.status(); print(s.ok); } catch(e) { print(0); }
" 2>/dev/null || echo "0")

if [ "${RS_STATUS}" = "1" ]; then
  echo "==> Replica Set rs-grupo ya inicializado. Nada que hacer."
  mongosh --host localhost:27017 --quiet --eval "
    rs.status().members.forEach(function(m) {
      print('  ' + m.name + ' -> ' + m.stateStr);
    });
  "
  exit 0
fi

# ── 4. Inicializar el Replica Set ────────────────────────────
echo "==> Inicializando rs-grupo con:"
echo "    Miembro 0: ${RS_MEMBER_1}"
echo "    Miembro 1: ${RS_MEMBER_2}"
echo "    Miembro 2: ${RS_MEMBER_3} (coordinador, priority=2)"

mongosh --host localhost:27017 --quiet --eval "
  rs.initiate({
    _id: 'rs-grupo',
    members: [
      { _id: 0, host: '${RS_MEMBER_1}', priority: 1 },
      { _id: 1, host: '${RS_MEMBER_2}', priority: 1 },
      { _id: 2, host: '${RS_MEMBER_3}', priority: 2 }
    ]
  });
"

# ── 5. Esperar la elección del primario ─────────────────────
echo "==> Esperando elección del PRIMARY..."
for i in $(seq 1 40); do
  IS_PRIMARY=$(mongosh --host localhost:27017 --quiet --eval "
    try {
      var members = rs.status().members;
      print(members.some(function(m) { return m.stateStr === 'PRIMARY'; }));
    } catch(e) { print(false); }
  " 2>/dev/null || echo "false")

  if [ "${IS_PRIMARY}" = "true" ]; then
    echo "    PRIMARY elegido."
    break
  fi
  echo "   ... esperando PRIMARY (${i}/40)"
  sleep 3
done

# ── 6. Estado final ──────────────────────────────────────────
echo "==> Estado final del Replica Set rs-grupo:"
mongosh --host localhost:27017 --quiet --eval "
  rs.status().members.forEach(function(m) {
    print('  ' + m.name + ' -> ' + m.stateStr);
  });
"
echo "==> Inicialización completada."
