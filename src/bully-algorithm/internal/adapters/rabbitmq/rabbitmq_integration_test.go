package rabbitmqadapter

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

// Estas pruebas requieren un broker real. Se omiten si RABBITMQ_TEST_URL no está
// definida. Usan colas con prefijo único y las eliminan al terminar.

// signalProcessor reporta por canal los mensajes que el consumidor confirma.
type signalProcessor struct {
	fakeProcessor
	applied chan model.MessageID
}

func (p *signalProcessor) DrainReady(ctx context.Context) ([]model.MessageID, error) {
	done, err := p.fakeProcessor.DrainReady(ctx)
	for _, id := range done {
		p.applied <- id
	}
	return done, err
}

func TestIntegrationPublishThenConsumeAcksMessage(t *testing.T) {
	url := os.Getenv("RABBITMQ_TEST_URL")
	if url == "" {
		t.Skip("RABBITMQ_TEST_URL not set")
	}
	topo := Topology{Prefix: fmt.Sprintf("test-%d-", time.Now().UnixNano())}
	cleanupQueues(t, url, topo)

	publisher, err := NewPublisher(PublisherConfig{
		URL:            url,
		Topology:       topo,
		DialTimeout:    5 * time.Second,
		ConfirmTimeout: 5 * time.Second,
		Retry:          DefaultRetryPolicy(),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer publisher.Close()

	msg := model.MetricReport{MessageID: "int-m-1", Type: model.TypeMetricReport, NodeID: 1, LamportTS: 5, Timestamp: time.Now().UTC(), CPUTempC: 40, FreeRAMMB: 512, BandwidthMbps: 50}
	publishCtx, cancelPublish := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelPublish()
	if err := publisher.Publish(publishCtx, msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	proc := &signalProcessor{fakeProcessor: fakeProcessor{accept: true, ready: true}, applied: make(chan model.MessageID, 10)}
	consumer, err := NewConsumer(ConsumerConfig{
		URL:           url,
		Topology:      topo,
		DialTimeout:   5 * time.Second,
		Prefetch:      10,
		DrainInterval: 20 * time.Millisecond,
		Reconnect:     DefaultRetryPolicy(),
	}, func() (MessageProcessor, error) { return proc, nil })
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- consumer.Run(runCtx) }()
	defer func() {
		cancelRun()
		<-runDone
	}()

	select {
	case id := <-proc.applied:
		if id != msg.MessageID {
			t.Fatalf("applied %s, want %s", id, msg.MessageID)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("consumer did not apply the message")
	}

	// El ACK llega después de Drain: la cola debe quedar vacía.
	if !waitQueueEmpty(t, url, topo.Queue(model.KindMetrics), 10*time.Second) {
		t.Fatal("queue still holds the message after ACK")
	}
}

func waitQueueEmpty(t *testing.T, url, queue string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := dialBroker(url, 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		ch, err := conn.Channel()
		if err != nil {
			_ = conn.Close()
			t.Fatalf("channel: %v", err)
		}
		q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
		_ = ch.Close()
		_ = conn.Close()
		if err != nil {
			t.Fatalf("inspect %s: %v", queue, err)
		}
		if q.Messages == 0 {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func cleanupQueues(t *testing.T, url string, topo Topology) {
	t.Helper()
	t.Cleanup(func() {
		conn, err := dialBroker(url, 5*time.Second)
		if err != nil {
			return
		}
		defer conn.Close()
		ch, err := conn.Channel()
		if err != nil {
			return
		}
		defer ch.Close()
		for _, kind := range Kinds {
			_, _ = ch.QueueDelete(topo.Queue(kind), false, false, false)
		}
	})
}
