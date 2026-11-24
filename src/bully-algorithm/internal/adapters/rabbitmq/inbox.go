package rabbitmqadapter

import (
	"context"
	"log"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

// ackHandle es la parte de una entrega del broker que usa el inbox. amqp.Delivery la implementa.
type ackHandle interface {
	Ack(multiple bool) error
	Nack(multiple, requeue bool) error
}

// MessageProcessor es el subconjunto de usecase.EventProcessor que usa el inbox.
type MessageProcessor interface {
	Receive(msg model.Message) (bool, error)
	DrainReady(ctx context.Context) ([]model.MessageID, error)
}

// Inbox aplica la regla de confirmación del consumidor: un mensaje solo recibe
// ACK cuando el procesador lo aplicó. Las entregas pendientes se guardan por
// message_id hasta que DrainReady las devuelve.
//
// No es seguro para uso concurrente: lo usa un único bucle de consumo.
type Inbox struct {
	processor MessageProcessor
	pending   map[model.MessageID]ackHandle
}

// NewInbox crea un inbox sobre el procesador de eventos.
func NewInbox(processor MessageProcessor) *Inbox {
	return &Inbox{processor: processor, pending: make(map[model.MessageID]ackHandle)}
}

// Deliver procesa una entrega recibida del broker.
//   - Payload inválido: Nack sin reencolar, para no repetirlo en bucle.
//   - Mensaje tardío o ya aplicado: ACK inmediato.
//   - Mensaje bufferizado: se guarda la entrega y se confirma en Drain.
//   - Reentrega de un mensaje pendiente: la entrega nueva reemplaza a la anterior.
func (in *Inbox) Deliver(kind model.Kind, body []byte, handle ackHandle) {
	msg, err := DecodeMessage(kind, body)
	if err != nil {
		log.Printf("rabbitmq: rejecting undecodable message topic=%s: %v", kind, err)
		_ = handle.Nack(false, false)
		return
	}

	if previous, ok := in.pending[msg.ID()]; ok {
		// La entrega anterior ya no se necesita: el mensaje se confirma por la nueva.
		_ = previous.Ack(false)
		in.pending[msg.ID()] = handle
		return
	}

	accepted, err := in.processor.Receive(msg)
	if err != nil {
		log.Printf("rabbitmq: rejecting invalid message id=%s topic=%s: %v", msg.ID(), kind, err)
		_ = handle.Nack(false, false)
		return
	}
	if !accepted {
		// Llegó tarde o ya fue aplicado: no hay nada más que hacer con él.
		_ = handle.Ack(false)
		return
	}
	in.pending[msg.ID()] = handle
}

// Drain aplica los mensajes listos y confirma los que el procesador devolvió.
// Si el procesador falla, los mensajes quedan pendientes para el siguiente Drain.
func (in *Inbox) Drain(ctx context.Context) error {
	done, err := in.processor.DrainReady(ctx)
	for _, id := range done {
		if handle, ok := in.pending[id]; ok {
			if ackErr := handle.Ack(false); ackErr != nil {
				log.Printf("rabbitmq: ack of message id=%s failed: %v", id, ackErr)
			}
			delete(in.pending, id)
		}
	}
	return err
}

// Pending devuelve la cantidad de entregas sin confirmar.
func (in *Inbox) Pending() int {
	return len(in.pending)
}

// Release devuelve al broker todas las entregas sin confirmar. Se usa al dejar
// de ser coordinador.
func (in *Inbox) Release() {
	for id, handle := range in.pending {
		if err := handle.Nack(false, true); err != nil {
			log.Printf("rabbitmq: requeue of message id=%s failed: %v", id, err)
		}
		delete(in.pending, id)
	}
}
