package rabbitmqadapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// RetryPolicy define la espera exponencial entre reintentos. No tiene límite de
// intentos: el contexto del llamador decide cuándo rendirse.
type RetryPolicy struct {
	Initial time.Duration
	Max     time.Duration
}

// DefaultRetryPolicy espera de 500 ms hasta 30 s.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{Initial: 500 * time.Millisecond, Max: 30 * time.Second}
}

func (p RetryPolicy) validate() error {
	if p.Initial <= 0 {
		return errors.New("retry initial backoff must be greater than zero")
	}
	if p.Max < p.Initial {
		return errors.New("retry max backoff must not be below initial backoff")
	}
	return nil
}

// backoff devuelve la espera tras el intento número attempt (desde 1).
func (p RetryPolicy) backoff(attempt int) time.Duration {
	delay := p.Initial
	for i := 1; i < attempt && delay < p.Max; i++ {
		delay *= 2
	}
	if delay > p.Max {
		return p.Max
	}
	return delay
}

// dialBroker abre una conexión AMQP con timeout de conexión.
func dialBroker(url string, timeout time.Duration) (*amqp.Connection, error) {
	if url == "" {
		return nil, errors.New("RabbitMQ URL must not be empty")
	}
	conn, err := amqp.DialConfig(url, amqp.Config{Dial: amqp.DefaultDial(timeout)})
	if err != nil {
		return nil, fmt.Errorf("dial RabbitMQ: %w", err)
	}
	return conn, nil
}

// declareTopology declara las tres colas durables del flujo asíncrono.
// Declararlas es idempotente, así que lo hacen tanto el publicador como el consumidor.
func declareTopology(ch *amqp.Channel, topo Topology) error {
	for _, kind := range Kinds {
		if _, err := ch.QueueDeclare(topo.Queue(kind), true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare queue %s: %w", topo.Queue(kind), err)
		}
	}
	return nil
}

// sleepContext espera d o hasta que el contexto termine.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
