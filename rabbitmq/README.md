# RabbitMQ del Taller 2

Este módulo levanta RabbitMQ mediante Docker Compose para el flujo asíncrono del Taller 2.

La configuración mantiene las tres colas definidas por el taller:

- `metrics`
- `allocations`
- `failover`

Por ahora se utiliza el exchange predeterminado de RabbitMQ. No se agrega un exchange personalizado como `system.events`.

## 1. Relación con el código Go

El código actual del proyecto utiliza RabbitMQ mediante la variable:

```env
RABBITMQ_URL
