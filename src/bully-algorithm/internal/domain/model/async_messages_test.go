package model

import (
	"testing"
	"time"
)

func validMetric() MetricReport {
	return MetricReport{
		MessageID:     "m-1",
		Type:          TypeMetricReport,
		NodeID:        2,
		LamportTS:     5,
		Timestamp:     time.Date(2026, 5, 10, 14, 0, 0, 0, time.UTC),
		CPUTempC:      55,
		FreeRAMMB:     1024,
		BandwidthMbps: 100,
	}
}

func validAllocation() AllocationRequest {
	return AllocationRequest{
		MessageID:  "a-1",
		Type:       TypeAllocationRequest,
		SourceNode: 1,
		Resource:   Resource{CPU: 1, RAMMB: 512, BandwidthMbps: 10},
		LamportTS:  7,
		SagaID:     "saga-1",
	}
}

func TestEventKeyOrdersByLamportThenNode(t *testing.T) {
	cases := []struct {
		name string
		a, b EventKey
		want bool
	}{
		{"lower lamport first", EventKey{3, 9}, EventKey{4, 1}, true},
		{"higher lamport last", EventKey{5, 1}, EventKey{4, 9}, false},
		{"same lamport lower node first", EventKey{5, 1}, EventKey{5, 2}, true},
		{"same key is not less", EventKey{5, 2}, EventKey{5, 2}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Less(tc.b); got != tc.want {
				t.Fatalf("Less = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMetricReportValidate(t *testing.T) {
	if err := validMetric().Validate(); err != nil {
		t.Fatalf("valid metric rejected: %v", err)
	}

	cases := map[string]func(*MetricReport){
		"missing message id": func(m *MetricReport) { m.MessageID = "" },
		"wrong type":         func(m *MetricReport) { m.Type = "OTHER" },
		"zero lamport":       func(m *MetricReport) { m.LamportTS = 0 },
		"zero node":          func(m *MetricReport) { m.NodeID = 0 },
		"zero timestamp":     func(m *MetricReport) { m.Timestamp = time.Time{} },
		"cpu temp above max": func(m *MetricReport) { m.CPUTempC = MaxCPUTempC + 1 },
		"cpu temp negative":  func(m *MetricReport) { m.CPUTempC = -1 },
		"cpu temp NaN":       func(m *MetricReport) { m.CPUTempC = nan() },
		"zero free ram":      func(m *MetricReport) { m.FreeRAMMB = 0 },
		"negative bandwidth": func(m *MetricReport) { m.BandwidthMbps = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := validMetric()
			mutate(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestMetricReportSupportsRAMAndBandwidth(t *testing.T) {
	m := validMetric() // 1024 MB libres, 100 Mbps
	if !m.Supports(Resource{CPU: 1, RAMMB: 1024, BandwidthMbps: 100}) {
		t.Fatal("exact fit should be supported")
	}
	if m.Supports(Resource{CPU: 1, RAMMB: 1025, BandwidthMbps: 10}) {
		t.Fatal("insufficient RAM should not be supported")
	}
	if m.Supports(Resource{CPU: 1, RAMMB: 10, BandwidthMbps: 101}) {
		t.Fatal("insufficient bandwidth should not be supported")
	}
}

func TestAllocationRequestValidate(t *testing.T) {
	if err := validAllocation().Validate(); err != nil {
		t.Fatalf("valid allocation rejected: %v", err)
	}
	cases := map[string]func(*AllocationRequest){
		"wrong type":   func(a *AllocationRequest) { a.Type = TypeNodeFailure },
		"missing saga": func(a *AllocationRequest) { a.SagaID = "" },
		"zero source":  func(a *AllocationRequest) { a.SourceNode = 0 },
		"zero cpu":     func(a *AllocationRequest) { a.Resource.CPU = 0 },
		"zero ram":     func(a *AllocationRequest) { a.Resource.RAMMB = 0 },
		"negative bw":  func(a *AllocationRequest) { a.Resource.BandwidthMbps = -5 },
		"zero lamport": func(a *AllocationRequest) { a.LamportTS = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a := validAllocation()
			mutate(&a)
			if err := a.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestFailoverEventValidate(t *testing.T) {
	valid := FailoverEvent{MessageID: "f-1", Type: TypeLeaderElected, PreviousLeader: 3, NewLeader: 2, LamportTS: 9}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid failover rejected: %v", err)
	}
	failure := valid
	failure.Type = TypeNodeFailure
	if err := failure.Validate(); err != nil {
		t.Fatalf("NODE_FAILURE should be accepted: %v", err)
	}

	unknown := valid
	unknown.Type = "OTHER"
	if err := unknown.Validate(); err == nil {
		t.Fatal("unknown failover type should be rejected")
	}
	noPrevious := valid
	noPrevious.PreviousLeader = 0
	if err := noPrevious.Validate(); err == nil {
		t.Fatal("missing previous_leader should be rejected")
	}
}

func TestMessageKeysUseDocumentedNode(t *testing.T) {
	if got := validMetric().Key(); got != (EventKey{LamportTS: 5, NodeID: 2}) {
		t.Fatalf("metric key = %+v", got)
	}
	if got := validAllocation().Key(); got != (EventKey{LamportTS: 7, NodeID: 1}) {
		t.Fatalf("allocation key = %+v", got)
	}
	f := FailoverEvent{MessageID: "f", Type: TypeLeaderElected, PreviousLeader: 3, NewLeader: 2, LamportTS: 9}
	if got := f.Key(); got != (EventKey{LamportTS: 9, NodeID: 2}) {
		t.Fatalf("failover key = %+v", got)
	}
}

func nan() float64 {
	zero := 0.0
	return zero / zero
}
