package rabbitmqadapter

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

// PublisherConfig agrupa los parámetros del publicador.
type PublisherConfig struct {
	URL            string
	Topology       Topology
	DialTimeout    time.Duration
	ConfirmTimeout time.Duration
	Retry          RetryPolicy
}

// Publisher envía mensajes del dominio al broker. Cada publicación espera la
// confirmación del broker. Si la conexión cae o el broker no confirma, reconecta
// y reintenta con backoff hasta que el contexto termine. Un reintento reenvía el
// mismo message_id: el consumidor lo descarta si ya lo procesó.
type Publisher struct {
	cfg   PublisherConfig
	sleep func(context.Context, time.Duration) error

	mu   sync.Mutex // serializa las publicaciones: el canal de confirmaciones es secuencial
	conn *amqp.Connection
	ch   *amqp.Channel
}

// NewPublisher valida la configuración y crea el publicador. No abre conexión:
// la abre en la primera publicación.
func NewPublisher(cfg PublisherConfig) (*Publisher, error) {
	if cfg.URL == "" {
		return nil, errors.New("RabbitMQ URL must not be empty")
	}
	if cfg.DialTimeout <= 0 || cfg.ConfirmTimeout <= 0 {
		return nil, errors.New("dial and confirm timeouts must be greater than zero")
	}
	if err := cfg.Retry.validate(); err != nil {
		return nil, err
	}
	return &Publisher{cfg: cfg, sleep: sleepContext}, nil
}

// Publish envía el mensaje a la cola de su tópico y espera la confirmación.
func (p *Publisher) Publish(ctx context.Context, msg model.Message) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	body, err := EncodeMessage(msg)
	if err != nil {
		return err
	}
	queue := p.cfg.Topology.Queue(msg.Kind())

	p.mu.Lock()
	defer p.mu.Unlock()

	for attempt := 1; ; attempt++ {
		err := p.publishOnce(ctx, queue, msg.ID(), body)
		if err == nil {
			log.Printf("rabbitmq: published message=%s queue=%s attempt=%d", msg.ID(), queue, attempt)
			return nil
		}
		p.resetLocked()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("publish message %s to %s: %w (last error: %v)", msg.ID(), queue, ctxErr, err)
		}
		wait := p.cfg.Retry.backoff(attempt)
		log.Printf("rabbitmq: publish message=%s queue=%s attempt=%d failed, retrying in %s: %v", msg.ID(), queue, attempt, wait, err)
		if sleepErr := p.sleep(ctx, wait); sleepErr != nil {
			return fmt.Errorf("publish message %s: %w (last error: %v)", msg.ID(), sleepErr, err)
		}
	}
}

// Close cierra la conexión actual.
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeLocked()
}

func (p *Publisher) publishOnce(ctx context.Context, queue string, id model.MessageID, body []byte) error {
	ch, err := p.channelLocked()
	if err != nil {
		return err
	}
	confirmation, err := ch.PublishWithDeferredConfirmWithContext(ctx, "", queue, false, false, amqp.Publishing{
		MessageId:    string(id),
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now().UTC(),
		Body:         body,
	})
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, p.cfg.ConfirmTimeout)
	defer cancel()
	acked, err := confirmation.WaitContext(waitCtx)
	if err != nil {
		return fmt.Errorf("wait for confirmation: %w", err)
	}
	if !acked {
		return errors.New("broker rejected the message")
	}
	return nil
}

// channelLocked devuelve un canal en modo confirmaciones, reconectando si hace falta.
func (p *Publisher) channelLocked() (*amqp.Channel, error) {
	if p.conn != nil && !p.conn.IsClosed() && p.ch != nil && !p.ch.IsClosed() {
		return p.ch, nil
	}
	p.closeLocked()

	conn, err := dialBroker(p.cfg.URL, p.cfg.DialTimeout)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("enable publisher confirms: %w", err)
	}
	if err := declareTopology(ch, p.cfg.Topology); err != nil {
		_ = conn.Close()
		return nil, err
	}
	p.conn = conn
	p.ch = ch
	return ch, nil
}

func (p *Publisher) resetLocked() {
	_ = p.closeLocked()
}

func (p *Publisher) closeLocked() error {
	var err error
	if p.ch != nil {
		err = p.ch.Close()
		p.ch = nil
	}
	if p.conn != nil {
		if closeErr := p.conn.Close(); err == nil {
			err = closeErr
		}
		p.conn = nil
	}
	return err
}
