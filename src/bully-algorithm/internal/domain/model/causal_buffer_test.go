package model

import (
	"errors"
	"testing"
	"time"
)

func TestCausalBufferReleasesInTotalOrderAfterWindow(t *testing.T) {
	buf, err := NewCausalBuffer[string](3 * time.Second)
	if err != nil {
		t.Fatalf("NewCausalBuffer: %v", err)
	}
	t0 := time.Date(2026, 5, 10, 14, 0, 0, 0, time.UTC)

	must(t, buf.Push(EventKey{5, 2}, t0, "a"))
	must(t, buf.Push(EventKey{5, 1}, t0.Add(1*time.Second), "b"))
	must(t, buf.Push(EventKey{4, 3}, t0.Add(2*time.Second), "c"))

	if _, ready := buf.Next(t0.Add(2 * time.Second)); ready {
		t.Fatal("nothing should be ready before the head window expires")
	}

	var got []string
	now := t0.Add(5 * time.Second)
	for {
		v, ready := buf.Next(now)
		if !ready {
			break
		}
		got = append(got, v)
		buf.Release()
	}
	want := []string{"c", "b", "a"}
	if len(got) != len(want) {
		t.Fatalf("released %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("released %v, want %v", got, want)
		}
	}
}

func TestCausalBufferBlocksHeadUntilItsWindowExpires(t *testing.T) {
	buf, _ := NewCausalBuffer[string](3 * time.Second)
	t0 := time.Date(2026, 5, 10, 14, 0, 0, 0, time.UTC)
	must(t, buf.Push(EventKey{1, 1}, t0, "first"))

	if _, ready := buf.Next(t0.Add(2999 * time.Millisecond)); ready {
		t.Fatal("event released before its window expired")
	}
	if v, ready := buf.Next(t0.Add(3 * time.Second)); !ready || v != "first" {
		t.Fatalf("Next = %q,%v, want first,true", v, ready)
	}
}

func TestCausalBufferDiscardsLateEvent(t *testing.T) {
	buf, _ := NewCausalBuffer[string](0)
	t0 := time.Date(2026, 5, 10, 14, 0, 0, 0, time.UTC)
	must(t, buf.Push(EventKey{5, 1}, t0, "released"))
	buf.Release()

	if err := buf.Push(EventKey{4, 9}, t0, "late"); !errors.Is(err, ErrLateEvent) {
		t.Fatalf("Push lower key = %v, want ErrLateEvent", err)
	}
	if err := buf.Push(EventKey{5, 1}, t0, "same key"); !errors.Is(err, ErrLateEvent) {
		t.Fatalf("Push equal key = %v, want ErrLateEvent", err)
	}
	if err := buf.Push(EventKey{5, 2}, t0, "next"); err != nil {
		t.Fatalf("Push greater key = %v, want nil", err)
	}
}

func TestCausalBufferRejectsDuplicateKeyWhileBuffered(t *testing.T) {
	buf, _ := NewCausalBuffer[string](time.Second)
	t0 := time.Now()
	must(t, buf.Push(EventKey{3, 1}, t0, "a"))
	if err := buf.Push(EventKey{3, 1}, t0, "b"); !errors.Is(err, ErrKeyCollision) {
		t.Fatalf("Push duplicate = %v, want ErrKeyCollision", err)
	}
}

func TestCausalBufferRejectsNegativeWindow(t *testing.T) {
	if _, err := NewCausalBuffer[string](-time.Second); err == nil {
		t.Fatal("negative window should be rejected")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
