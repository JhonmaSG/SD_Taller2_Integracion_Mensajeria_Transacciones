package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

// Garantías de comprobación en compilación.
var (
	_ ports.ProcessedMessageStore = (*AsyncStore)(nil)
	_ ports.SagaRepository        = (*AsyncStore)(nil)
	_ ports.AllocationRepository  = (*AsyncStore)(nil)
	_ ports.FailoverRepository    = (*AsyncStore)(nil)
	_ ports.MetricsRepository     = (*AsyncStore)(nil)
)

// AsyncStore implementa los puertos de persistencia del flujo asíncrono sobre
// MongoDB. Las escrituras de sagas usan CAS por version. Las demás son
// idempotentes por su clave natural (message_id, saga_id).
type AsyncStore struct {
	client    *mongo.Client
	sagas     *mongo.Collection
	processed *mongo.Collection
	allocs    *mongo.Collection
	failovers *mongo.Collection
	metrics   *mongo.Collection
}

// NewAsyncStore conecta a MongoDB con write concern majority y crea los índices.
func NewAsyncStore(ctx context.Context, uri, databaseName string) (*AsyncStore, error) {
	if ctx == nil {
		return nil, errors.New("context must not be nil")
	}
	if uri == "" || databaseName == "" {
		return nil, errors.New("MongoDB URI and database name must not be empty")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, fmt.Errorf("connect to MongoDB: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("ping MongoDB: %w", err)
	}
	store, err := newAsyncStore(ctx, client, client.Database(databaseName))
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return store, nil
}

func newAsyncStore(ctx context.Context, client *mongo.Client, db *mongo.Database) (*AsyncStore, error) {
	s := &AsyncStore{
		client:    client,
		sagas:     db.Collection(asyncSagasCollection),
		processed: db.Collection(asyncProcessedCollection),
		allocs:    db.Collection(asyncAllocationsCollection),
		failovers: db.Collection(asyncFailoverCollection),
		metrics:   db.Collection(asyncNodeMetricsCollection),
	}
	if err := s.ensureIndexes(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Close desconecta el cliente. Si el almacén se creó sobre un cliente ajeno, no hace nada.
func (s *AsyncStore) Close(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.client.Disconnect(ctx)
}

func (s *AsyncStore) ensureIndexes(ctx context.Context) error {
	unique := func(field string) mongo.IndexModel {
		return mongo.IndexModel{
			Keys:    bson.D{{Key: field, Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uniq_" + field),
		}
	}
	plan := []struct {
		coll   *mongo.Collection
		models []mongo.IndexModel
	}{
		{s.processed, []mongo.IndexModel{unique("message_id")}},
		{s.sagas, []mongo.IndexModel{
			unique("saga_id"),
			{Keys: bson.D{{Key: "status", Value: 1}}, Options: options.Index().SetName("idx_status")},
			{Keys: bson.D{{Key: "status", Value: 1}, {Key: "notification.notified", Value: 1}}, Options: options.Index().SetName("idx_pending_notifications")},
		}},
		{s.allocs, []mongo.IndexModel{unique("saga_id")}},
		{s.failovers, []mongo.IndexModel{unique("event_id")}},
	}
	for _, p := range plan {
		if _, err := p.coll.Indexes().CreateMany(ctx, p.models); err != nil {
			return fmt.Errorf("create indexes on %s: %w", p.coll.Name(), err)
		}
	}
	return nil
}

// IsProcessed indica si el message_id ya está registrado.
func (s *AsyncStore) IsProcessed(ctx context.Context, id model.MessageID) (bool, error) {
	n, err := s.processed.CountDocuments(ctx, bson.M{"message_id": string(id)})
	if err != nil {
		return false, fmt.Errorf("count processed message %s: %w", id, err)
	}
	return n > 0, nil
}

// MarkProcessed inserta el registro. Si el message_id ya existe, devuelve model.ErrAlreadyProcessed.
func (s *AsyncStore) MarkProcessed(ctx context.Context, record ports.ProcessedRecord) error {
	_, err := s.processed.InsertOne(ctx, asyncProcessedToDoc(record))
	if mongo.IsDuplicateKeyError(err) {
		return model.ErrAlreadyProcessed
	}
	if err != nil {
		return fmt.Errorf("mark processed message %s: %w", record.MessageID, err)
	}
	return nil
}

// Find devuelve la saga con ese saga_id.
func (s *AsyncStore) Find(ctx context.Context, id model.SagaID) (model.Saga, bool, error) {
	var doc asyncSagaDoc
	err := s.sagas.FindOne(ctx, bson.M{"saga_id": string(id)}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return model.Saga{}, false, nil
	}
	if err != nil {
		return model.Saga{}, false, fmt.Errorf("find saga %s: %w", id, err)
	}
	saga, err := asyncSagaFromDoc(doc)
	if err != nil {
		return model.Saga{}, false, err
	}
	return saga, true, nil
}

// Save persiste la saga con CAS. Una saga nueva se inserta con version 1. Una
// existente solo se reemplaza si su version en MongoDB coincide con saga.Version.
// En caso contrario devuelve ports.ErrVersionConflict. Al escribir, actualiza saga.Version.
func (s *AsyncStore) Save(ctx context.Context, saga *model.Saga) error {
	if saga == nil {
		return errors.New("saga must not be nil")
	}
	if saga.Version == 0 {
		doc := asyncSagaToDoc(*saga)
		doc.Version = 1
		_, err := s.sagas.InsertOne(ctx, doc)
		if mongo.IsDuplicateKeyError(err) {
			return ports.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("insert saga %s: %w", saga.ID, err)
		}
		saga.Version = 1
		return nil
	}

	expected := int64(saga.Version)
	doc := asyncSagaToDoc(*saga)
	doc.Version = expected + 1
	result, err := s.sagas.ReplaceOne(ctx, bson.M{"saga_id": string(saga.ID), "version": expected}, doc)
	if err != nil {
		return fmt.Errorf("replace saga %s: %w", saga.ID, err)
	}
	if result.MatchedCount == 0 {
		return ports.ErrVersionConflict
	}
	saga.Version = uint64(expected + 1)
	return nil
}

// PendingNotifications devuelve las sagas COMPLETED sin notificar.
func (s *AsyncStore) PendingNotifications(ctx context.Context) ([]model.Saga, error) {
	cursor, err := s.sagas.Find(ctx, bson.M{"status": string(model.SagaCompleted), "notification.notified": false})
	if err != nil {
		return nil, fmt.Errorf("query pending notifications: %w", err)
	}
	var docs []asyncSagaDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode pending notifications: %w", err)
	}
	out := make([]model.Saga, 0, len(docs))
	for _, doc := range docs {
		saga, err := asyncSagaFromDoc(doc)
		if err != nil {
			return nil, err
		}
		out = append(out, saga)
	}
	return out, nil
}

// Replicate inserta la asignación de la saga. Si ya existe para ese saga_id, no
// modifica nada: la operación es idempotente.
func (s *AsyncStore) Replicate(ctx context.Context, saga model.Saga) error {
	doc := asyncAllocationDoc{
		SagaID:        string(saga.ID),
		SourceNode:    asyncNodeKey(saga.SourceNode),
		ReservationID: saga.ReservationID,
		Resource: asyncResourceDoc{
			CPU:       saga.Resource.CPU,
			RAM:       int64(saga.Resource.RAMMB),
			Bandwidth: saga.Resource.BandwidthMbps,
		},
		Lamport:   asyncLamportDoc{TS: int64(saga.LamportTS), NodeID: asyncNodeKey(saga.SourceNode)},
		CreatedAt: saga.CreatedAt,
	}
	update := bson.M{"$setOnInsert": bson.M{
		"source_node":    doc.SourceNode,
		"reservation_id": doc.ReservationID,
		"resource":       doc.Resource,
		"lamport":        doc.Lamport,
		"created_at":     doc.CreatedAt,
	}}
	if _, err := s.allocs.UpdateOne(ctx, bson.M{"saga_id": doc.SagaID}, update, options.UpdateOne().SetUpsert(true)); err != nil {
		return fmt.Errorf("replicate allocation %s: %w", saga.ID, err)
	}
	return nil
}

// Record inserta el evento de failover. Si el message_id ya existe, no hace nada.
func (s *AsyncStore) Record(ctx context.Context, event model.FailoverEvent) error {
	_, err := s.failovers.InsertOne(ctx, asyncFailoverToDoc(event, time.Now().UTC()))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("record failover %s: %w", event.MessageID, err)
	}
	return nil
}

// SaveLatest reemplaza el último reporte del nodo solo si su lamport_ts no es
// menor que el guardado. Un reporte más antiguo se ignora sin error.
func (s *AsyncStore) SaveLatest(ctx context.Context, report model.MetricReport) error {
	doc := asyncMetricToDoc(report)
	filter := bson.M{"_id": doc.ID, "lamport.ts": bson.M{"$lte": doc.Lamport.TS}}
	_, err := s.metrics.ReplaceOne(ctx, filter, doc, options.Replace().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("save metrics of node %d: %w", report.NodeID, err)
	}
	return nil
}

// Latest devuelve el último reporte del nodo.
func (s *AsyncStore) Latest(ctx context.Context, node model.NodeID) (model.MetricReport, bool, error) {
	var doc asyncMetricDoc
	err := s.metrics.FindOne(ctx, bson.M{"_id": asyncNodeKey(node)}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return model.MetricReport{}, false, nil
	}
	if err != nil {
		return model.MetricReport{}, false, fmt.Errorf("read metrics of node %d: %w", node, err)
	}
	report, err := asyncMetricFromDoc(doc)
	if err != nil {
		return model.MetricReport{}, false, err
	}
	return report, true, nil
}
