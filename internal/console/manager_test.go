package console

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenOptionsValidation(t *testing.T) {
	validated, err := (OpenOptions{
		Address:  " ilo.example.test ",
		Username: " operator ",
		Password: "secret",
	}).validate()
	if err != nil {
		t.Fatal(err)
	}
	if validated.Address != "ilo.example.test" || validated.Username != "operator" {
		t.Fatalf("unexpected normalized options: %#v", validated)
	}
	if validated.BusyMode != BusyFail {
		t.Fatalf("busy mode = %q, want %q", validated.BusyMode, BusyFail)
	}
	if _, err := (OpenOptions{Address: "ilo", Username: "operator"}).validate(); err == nil {
		t.Fatal("validation accepted an empty password")
	}
	if _, err := (OpenOptions{Address: "ilo", Username: "operator", Password: "secret", BusyMode: "share"}).validate(); err == nil {
		t.Fatal("validation accepted a shared console mode")
	}
}

func TestHandlesAreOpaqueAndUnique(t *testing.T) {
	first, err := newHandle()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newHandle()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("generated duplicate handles")
	}
	if len(first) != 43 || len(second) != 43 {
		t.Fatalf("unexpected handle lengths: %d and %d", len(first), len(second))
	}
}

func TestExecuteOnceDeduplicatesRetry(t *testing.T) {
	session := &Session{operations: make(map[string]*operationRecord)}
	var calls atomic.Int32
	operation := func() (any, error) {
		calls.Add(1)
		return "done", nil
	}
	for range 2 {
		value, err := session.ExecuteOnce(context.Background(), "operation-1", "same-input", operation)
		if err != nil {
			t.Fatal(err)
		}
		if value != "done" {
			t.Fatalf("value = %v, want done", value)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("operation called %d times, want once", calls.Load())
	}
	if _, err := session.ExecuteOnce(context.Background(), "operation-1", "different-input", operation); err == nil {
		t.Fatal("operation_id reuse with different arguments was accepted")
	}
}

func TestExecuteOnceAnswersARetryAfterAPanic(t *testing.T) {
	s := &Session{operations: make(map[string]*operationRecord)}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic did not travel out of ExecuteOnce")
			}
		}()
		_, _ = s.ExecuteOnce(context.Background(), "op-1", "key", func() (any, error) {
			panic("the operation blew up")
		})
	}()

	// A retry on the same operation_id used to wait on a channel that nobody
	// would ever close, so it only ended when its own context expired.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.ExecuteOnce(ctx, "op-1", "key", func() (any, error) { return "late", nil })
	if err == nil {
		t.Fatal("the retry reported success for an operation that panicked")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("the retry blocked until its context expired")
	}
}
