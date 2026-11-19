package model

import (
	"container/heap"
	"errors"
	"time"
)

var (
	// ErrLateEvent indica que el evento precede a uno ya liberado y se descarta.
	ErrLateEvent = errors.New("event precedes an already released event")
	// ErrKeyCollision indica que ya hay un evento en espera con la misma clave.
	ErrKeyCollision = errors.New("event key already buffered")
)

type bufferedEntry[T any] struct {
	key     EventKey
	arrival time.Time
	value   T
}

type entryHeap[T any] []bufferedEntry[T]

func (h entryHeap[T]) Len() int           { return len(h) }
func (h entryHeap[T]) Less(i, j int) bool { return h[i].key.Less(h[j].key) }
func (h entryHeap[T]) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *entryHeap[T]) Push(x any) {
	*h = append(*h, x.(bufferedEntry[T]))
}

func (h *entryHeap[T]) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

// CausalBuffer retiene los eventos durante una ventana de tiempo y los libera
// en orden estricto de (lamport_ts, node_id). Un evento solo se libera cuando
// su ventana venció y ningún evento con clave menor queda pendiente. Si llega
// un evento con clave menor o igual a la última liberada, se descarta con
// ErrLateEvent.
//
// No es seguro para uso concurrente: el llamador debe serializar el acceso.
type CausalBuffer[T any] struct {
	window       time.Duration
	queue        entryHeap[T]
	buffered     map[EventKey]struct{}
	lastReleased EventKey
	released     bool
}

// NewCausalBuffer crea un buffer con la ventana indicada. Una ventana de cero
// libera cada evento en cuanto se consulta.
func NewCausalBuffer[T any](window time.Duration) (*CausalBuffer[T], error) {
	if window < 0 {
		return nil, errors.New("causal window must not be negative")
	}
	return &CausalBuffer[T]{
		window:   window,
		buffered: make(map[EventKey]struct{}),
	}, nil
}

// Push encola un evento recibido en el instante arrival.
func (b *CausalBuffer[T]) Push(key EventKey, arrival time.Time, value T) error {
	if b.released && !b.lastReleased.Less(key) {
		return ErrLateEvent
	}
	if _, dup := b.buffered[key]; dup {
		return ErrKeyCollision
	}
	heap.Push(&b.queue, bufferedEntry[T]{key: key, arrival: arrival, value: value})
	b.buffered[key] = struct{}{}
	return nil
}

// Next devuelve el evento más antiguo en orden si su ventana ya venció en now.
// No lo retira: el llamador debe invocar Release tras procesarlo.
func (b *CausalBuffer[T]) Next(now time.Time) (T, bool) {
	var zero T
	if len(b.queue) == 0 {
		return zero, false
	}
	head := b.queue[0]
	if now.Before(head.arrival.Add(b.window)) {
		return zero, false
	}
	return head.value, true
}

// Release retira el evento devuelto por Next y registra su clave como liberada.
// Devuelve false si el buffer está vacío.
func (b *CausalBuffer[T]) Release() bool {
	if len(b.queue) == 0 {
		return false
	}
	head := heap.Pop(&b.queue).(bufferedEntry[T])
	delete(b.buffered, head.key)
	b.lastReleased = head.key
	b.released = true
	return true
}

// Len devuelve la cantidad de eventos en espera.
func (b *CausalBuffer[T]) Len() int {
	return len(b.queue)
}
