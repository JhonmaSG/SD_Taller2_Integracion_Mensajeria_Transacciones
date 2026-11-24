package persistence

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

// Estas pruebas requieren un MongoDB real. Se omiten si MONGO_TEST_URI no está
// definida. Crean una base temporal y la eliminan al terminar.
func newTestAsyncStore(t *testing.T) (*AsyncStore, *mongo.Database) {
	t.Helper()
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("MONGO_TEST_URI not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbName := fmt.Sprintf("sd_async_test_%d", time.Now().UnixNano())
	store, err := NewAsyncStore(ctx, uri, dbName)
	if err != nil {
		t.Fatalf("NewAsyncStore: %v", err)
	}
	db := store.sagas.Database()
	t.Cleanup(func() {
		cleanCtx, cancelClean := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelClean()
		_ = db.Drop(cleanCtx)
		_ = store.Close(cleanCtx)
	})
	return store, db
}

func TestAsyncStoreMarksProcessedOnlyOnce(t *testing.T) {
	store, _ := newTestAsyncStore(t)
	ctx := context.Background()
	record := ports.ProcessedRecord{MessageID: "msg-1", Topic: model.KindMetrics, NodeID: 1, LamportTS: 5, ProcessedAt: asyncTestTime}

	if err := store.MarkProcessed(ctx, record); err != nil {
		t.Fatalf("first MarkProcessed: %v", err)
	}
	seen, err := store.IsProcessed(ctx, "msg-1")
	if err != nil || !seen {
		t.Fatalf("IsProcessed = %v, %v; want true", seen, err)
	}
	if err := store.MarkProcessed(ctx, record); err != model.ErrAlreadyProcessed {
		t.Fatalf("second MarkProcessed = %v, want ErrAlreadyProcessed", err)
	}
}

func TestAsyncStoreSagaCompareAndSwap(t *testing.T) {
	store, _ := newTestAsyncStore(t)
	ctx := context.Background()

	saga := asyncTestSaga()
	if err := store.Save(ctx, &saga); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if saga.Version != 1 {
		t.Fatalf("version after insert = %d, want 1", saga.Version)
	}

	stale := saga // copia con version 1
	must(t, saga.MarkReserved("res-01", asyncTestTime))
	if err := store.Save(ctx, &saga); err != nil {
		t.Fatalf("update: %v", err)
	}
	if saga.Version != 2 {
		t.Fatalf("version after update = %d, want 2", saga.Version)
	}

	must(t, stale.MarkReserved("res-02", asyncTestTime))
	if err := store.Save(ctx, &stale); err != ports.ErrVersionConflict {
		t.Fatalf("stale save = %v, want ErrVersionConflict", err)
	}

	found, ok, err := store.Find(ctx, saga.ID)
	if err != nil || !ok {
		t.Fatalf("Find = %v, %v", ok, err)
	}
	if found.ReservationID != "res-01" || found.Version != 2 {
		t.Fatalf("stored saga = %+v, want res-01 version 2", found)
	}
}

func TestAsyncStorePendingNotificationsOnlyCompletedUnnotified(t *testing.T) {
	store, _ := newTestAsyncStore(t)
	ctx := context.Background()

	pending := asyncTestSaga()
	must(t, pending.MarkReserved("res-01", asyncTestTime))
	must(t, pending.Complete(asyncTestTime))
	if err := store.Save(ctx, &pending); err != nil {
		t.Fatal(err)
	}

	inProgress := asyncTestSaga()
	inProgress.ID = "saga-in-progress"
	if err := store.Save(ctx, &inProgress); err != nil {
		t.Fatal(err)
	}

	list, err := store.PendingNotifications(ctx)
	if err != nil {
		t.Fatalf("PendingNotifications: %v", err)
	}
	if len(list) != 1 || list[0].ID != pending.ID {
		t.Fatalf("pending = %+v, want only %s", list, pending.ID)
	}
}

func TestAsyncStoreReplicateAndFailoverAreIdempotent(t *testing.T) {
	store, db := newTestAsyncStore(t)
	ctx := context.Background()

	saga := asyncTestSaga()
	must(t, saga.MarkReserved("res-01", asyncTestTime))
	must(t, saga.Complete(asyncTestTime))
	for i := 0; i < 2; i++ {
		if err := store.Replicate(ctx, saga); err != nil {
			t.Fatalf("Replicate attempt %d: %v", i+1, err)
		}
	}
	if n, _ := db.Collection(asyncAllocationsCollection).CountDocuments(ctx, bson.M{"saga_id": string(saga.ID)}); n != 1 {
		t.Fatalf("allocations = %d, want 1", n)
	}

	event := model.FailoverEvent{MessageID: "fo-1", Type: model.TypeLeaderElected, PreviousLeader: 3, NewLeader: 2, LamportTS: 52}
	for i := 0; i < 2; i++ {
		if err := store.Record(ctx, event); err != nil {
			t.Fatalf("Record attempt %d: %v", i+1, err)
		}
	}
	if n, _ := db.Collection(asyncFailoverCollection).CountDocuments(ctx, bson.M{"event_id": "fo-1"}); n != 1 {
		t.Fatalf("failover_events = %d, want 1", n)
	}
}

func TestAsyncStoreIgnoresOlderMetricReport(t *testing.T) {
	store, _ := newTestAsyncStore(t)
	ctx := context.Background()

	newer := model.MetricReport{MessageID: "m-new", Type: model.TypeMetricReport, NodeID: 1, LamportTS: 9, Timestamp: asyncTestTime, CPUTempC: 50, FreeRAMMB: 2048, BandwidthMbps: 100}
	older := newer
	older.MessageID = "m-old"
	older.LamportTS = 4
	older.FreeRAMMB = 10

	must(t, store.SaveLatest(ctx, newer))
	must(t, store.SaveLatest(ctx, older))

	got, ok, err := store.Latest(ctx, 1)
	if err != nil || !ok {
		t.Fatalf("Latest = %v, %v", ok, err)
	}
	if got.MessageID != "m-new" || got.FreeRAMMB != 2048 {
		t.Fatalf("latest = %+v, want m-new", got)
	}
}
