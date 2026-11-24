package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

type publishClockFake struct{ value model.LamportTime }

func (c *publishClockFake) Tick() model.LamportTime {
	c.value++
	return c.value
}

func (c *publishClockFake) Observe(remote model.LamportTime) model.LamportTime {
	if remote > c.value {
		c.value = remote
	}
	return c.Tick()
}

type publisherFake struct {
	published []model.Message
	err       error
}

func (p *publisherFake) Publish(_ context.Context, msg model.Message) error {
	if p.err != nil {
		return p.err
	}
	p.published = append(p.published, msg)
	return nil
}

type samplerFake struct {
	metrics ports.MachineMetrics
	err     error
}

func (s samplerFake) ReadMetrics(_ context.Context) (ports.MachineMetrics, error) {
	return s.metrics, s.err
}

var publishNow = time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC)

func publishDeps(clock *publishClockFake, pub *publisherFake) PublishDeps {
	return PublishDeps{
		NodeID:    2,
		Clock:     clock,
		Publisher: pub,
		NewID:     func() model.MessageID { return "fixed-id" },
		Now:       func() time.Time { return publishNow },
	}
}

func TestMetricsReporterPublishesReadingsWithLamportStamp(t *testing.T) {
	clock := &publishClockFake{value: 47}
	pub := &publisherFake{}
	reporter, err := NewMetricsReporter(publishDeps(clock, pub), samplerFake{metrics: ports.MachineMetrics{CPUTempC: 55, FreeRAMMB: 1024, BandwidthMbps: 100}}, nil)
	if err != nil {
		t.Fatalf("NewMetricsReporter: %v", err)
	}

	if err := reporter.Report(context.Background()); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published %d messages, want 1", len(pub.published))
	}
	report, ok := pub.published[0].(model.MetricReport)
	if !ok {
		t.Fatalf("published %T, want MetricReport", pub.published[0])
	}
	if report.LamportTS != 48 || report.NodeID != 2 || report.MessageID != "fixed-id" || report.Type != model.TypeMetricReport {
		t.Fatalf("report = %+v", report)
	}
	if report.FreeRAMMB != 1024 || report.CPUTempC != 55 || report.BandwidthMbps != 100 || !report.Timestamp.Equal(publishNow) {
		t.Fatalf("readings not copied: %+v", report)
	}
}

func TestMetricsReporterReturnsSamplerError(t *testing.T) {
	pub := &publisherFake{}
	reporter, _ := NewMetricsReporter(publishDeps(&publishClockFake{}, pub), samplerFake{err: errors.New("no sensor")}, nil)
	if err := reporter.Report(context.Background()); err == nil {
		t.Fatal("sampler failure should be returned")
	}
	if len(pub.published) != 0 {
		t.Fatal("nothing should be published when the sampler fails")
	}
}

func TestLeadershipAnnouncerPublishesLeaderElected(t *testing.T) {
	clock := &publishClockFake{value: 51}
	pub := &publisherFake{}
	announcer, err := NewLeadershipAnnouncer(publishDeps(clock, pub))
	if err != nil {
		t.Fatalf("NewLeadershipAnnouncer: %v", err)
	}

	if err := announcer.AnnounceLeaderElected(context.Background(), 3); err != nil {
		t.Fatalf("AnnounceLeaderElected: %v", err)
	}
	event, ok := pub.published[0].(model.FailoverEvent)
	if !ok {
		t.Fatalf("published %T, want FailoverEvent", pub.published[0])
	}
	want := model.FailoverEvent{MessageID: "fixed-id", Type: model.TypeLeaderElected, PreviousLeader: 3, NewLeader: 2, LamportTS: 52}
	if event != want {
		t.Fatalf("event = %+v, want %+v", event, want)
	}
}

func TestPublishUseCasesWrapPublisherErrors(t *testing.T) {
	pub := &publisherFake{err: errors.New("broker down")}
	announcer, _ := NewLeadershipAnnouncer(publishDeps(&publishClockFake{}, pub))
	if err := announcer.AnnounceLeaderElected(context.Background(), 3); err == nil {
		t.Fatal("publisher error should be returned")
	}
}

func TestPublishUseCasesValidateDependencies(t *testing.T) {
	if _, err := NewLeadershipAnnouncer(PublishDeps{NodeID: 0}); err == nil {
		t.Fatal("zero node id should fail")
	}
	if _, err := NewMetricsReporter(publishDeps(&publishClockFake{}, &publisherFake{}), nil, nil); err == nil {
		t.Fatal("nil sampler should fail")
	}
}

type countingSampler struct{ calls int }

func (s *countingSampler) ReadMetrics(_ context.Context) (ports.MachineMetrics, error) {
	s.calls++
	return ports.MachineMetrics{CPUTempC: 40, FreeRAMMB: 512, BandwidthMbps: 100}, nil
}

func TestMetricsReporterSkipsWhenCoordinator(t *testing.T) {
	pub := &publisherFake{}
	sampler := &countingSampler{}
	reporter, err := NewMetricsReporter(publishDeps(&publishClockFake{}, pub), sampler, func() bool { return true })
	if err != nil {
		t.Fatalf("NewMetricsReporter: %v", err)
	}

	err = reporter.Report(context.Background())
	if !errors.Is(err, ErrCoordinatorSkipsMetrics) {
		t.Fatalf("Report error = %v, want ErrCoordinatorSkipsMetrics", err)
	}
	if len(pub.published) != 0 {
		t.Fatalf("coordinator published %d messages, want 0", len(pub.published))
	}
	if sampler.calls != 0 {
		t.Fatalf("coordinator read the sensor %d times, want 0", sampler.calls)
	}
}

func TestMetricsReporterResumesPublishingAfterLosingCoordinatorRole(t *testing.T) {
	pub := &publisherFake{}
	coordinator := true
	reporter, err := NewMetricsReporter(publishDeps(&publishClockFake{}, pub), &countingSampler{}, func() bool { return coordinator })
	if err != nil {
		t.Fatalf("NewMetricsReporter: %v", err)
	}

	if err := reporter.Report(context.Background()); !errors.Is(err, ErrCoordinatorSkipsMetrics) {
		t.Fatalf("Report as coordinator error = %v, want ErrCoordinatorSkipsMetrics", err)
	}
	coordinator = false
	if err := reporter.Report(context.Background()); err != nil {
		t.Fatalf("Report as follower: %v", err)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published %d messages after role change, want 1", len(pub.published))
	}
}

func TestMetricsReporterWithoutCoordinatorCheckPublishes(t *testing.T) {
	pub := &publisherFake{}
	reporter, err := NewMetricsReporter(publishDeps(&publishClockFake{}, pub), &countingSampler{}, nil)
	if err != nil {
		t.Fatalf("NewMetricsReporter: %v", err)
	}
	if err := reporter.Report(context.Background()); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published %d messages, want 1", len(pub.published))
	}
}

func TestNewRandomMessageIDIsUniqueAndNonEmpty(t *testing.T) {
	a, b := NewRandomMessageID(), NewRandomMessageID()
	if a == "" || a == b {
		t.Fatalf("ids %q and %q should be non-empty and different", a, b)
	}
}
