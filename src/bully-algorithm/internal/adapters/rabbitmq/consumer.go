package rabbitmqadapter

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

// ConsumerConfig agrupa los parámetros del consumidor.
type ConsumerConfig struct {
	URL         string
	Topology    Topology
	DialTimeout time.Duration
	// Prefetch limita las entregas sin confirmar. Debe ser suficiente para cubrir
	// los mensajes que llegan dentro de la ventana de orden.
	Prefetch      int
	DrainInterval time.Duration
	Reconnect     RetryPolicy
}

// Consumer lee las tres colas y las entrega al inbox. Solo debe ejecutarse en el
// nodo coordinador: el llamador inicia Run al ser elegido y cancela su contexto
// al perder el rol. Al cancelarse, las entregas pendientes vuelven a la cola.
//
// newProcessor crea un procesador nuevo en cada ejecución de Run. Así un
// coordinador que vuelve a serlo no conserva un buffer viejo.
type Consumer struct {
	cfg          ConsumerConfig
	newProcessor func() (MessageProcessor, error)
}

// NewConsumer valida la configuración y crea el consumidor.
func NewConsumer(cfg ConsumerConfig, newProcessor func() (MessageProcessor, error)) (*Consumer, error) {
	if cfg.URL == "" {
		return nil, errors.New("RabbitMQ URL must not be empty")
	}
	if newProcessor == nil {
		return nil, errors.New("processor factory must not be nil")
	}
	if cfg.DialTimeout <= 0 {
		return nil, errors.New("dial timeout must be greater than zero")
	}
	if cfg.Prefetch <= 0 {
		return nil, errors.New("prefetch must be greater than zero")
	}
	if cfg.DrainInterval <= 0 {
		return nil, errors.New("drain interval must be greater than zero")
	}
	if err := cfg.Reconnect.validate(); err != nil {
		return nil, err
	}
	return &Consumer{cfg: cfg, newProcessor: newProcessor}, nil
}

// Run consume mientras ctx esté activo. Si la conexión cae, reconecta con backoff.
// Devuelve nil al cancelarse ctx.
func (c *Consumer) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	processor, err := c.newProcessor()
	if err != nil {
		return fmt.Errorf("create event processor: %w", err)
	}
	inbox := NewInbox(processor)

	for attempt := 1; ; attempt++ {
		established, err := c.session(ctx, inbox)
		if ctx.Err() != nil {
			return nil
		}
		if established {
			attempt = 1
		}
		wait := c.cfg.Reconnect.backoff(attempt)
		log.Printf("rabbitmq: consumer session ended, reconnecting in %s: %v", wait, err)
		if sleepErr := sleepContext(ctx, wait); sleepErr != nil {
			return nil
		}
	}
}

// session abre una conexión y consume hasta que se cancela ctx o se pierde la
// conexión. Devuelve established=true si llegó a consumir.
func (c *Consumer) session(ctx context.Context, inbox *Inbox) (bool, error) {
	conn, err := dialBroker(c.cfg.URL, c.cfg.DialTimeout)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return false, fmt.Errorf("open channel: %w", err)
	}
	if err := declareTopology(ch, c.cfg.Topology); err != nil {
		return false, err
	}
	if err := ch.Qos(c.cfg.Prefetch, 0, false); err != nil {
		return false, fmt.Errorf("set qos: %w", err)
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	incoming := make(chan inboundDelivery)
	for _, kind := range Kinds {
		deliveries, err := ch.Consume(c.cfg.Topology.Queue(kind), "", false, false, false, false, nil)
		if err != nil {
			return true, fmt.Errorf("consume queue %s: %w", c.cfg.Topology.Queue(kind), err)
		}
		go forwardDeliveries(sessionCtx, kind, deliveries, incoming)
	}

	connClosed := conn.NotifyClose(make(chan *amqp.Error, 1))
	chClosed := ch.NotifyClose(make(chan *amqp.Error, 1))
	ticker := time.NewTicker(c.cfg.DrainInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Se devuelven las entregas antes de cerrar la conexión para que el broker
			// las reencole sin esperar al cierre.
			inbox.Release()
			return true, nil
		case amqpErr := <-connClosed:
			return true, fmt.Errorf("connection closed: %v", amqpErr)
		case amqpErr := <-chClosed:
			return true, fmt.Errorf("channel closed: %v", amqpErr)
		case in := <-incoming:
			inbox.Deliver(in.kind, in.delivery.Body, in.delivery)
		case <-ticker.C:
			if err := inbox.Drain(ctx); err != nil {
				log.Printf("rabbitmq: drain failed, messages stay pending: %v", err)
			}
		}
	}
}

type inboundDelivery struct {
	kind     model.Kind
	delivery amqp.Delivery
}

// forwardDeliveries reenvía las entregas de una cola al canal común de la sesión.
func forwardDeliveries(ctx context.Context, kind model.Kind, deliveries <-chan amqp.Delivery, out chan<- inboundDelivery) {
	for {
		select {
		case <-ctx.Done():
			return
		case delivery, ok := <-deliveries:
			if !ok {
				return
			}
			select {
			case out <- inboundDelivery{kind: kind, delivery: delivery}:
			case <-ctx.Done():
				return
			}
		}
	}
}
