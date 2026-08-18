package console

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestObserveWaitsForFirstFramebufferRevision(t *testing.T) {
	session := &Session{
		connected: true,
		notify:    make(chan struct{}),
	}
	started := time.Now()
	state, image, err := session.Observe(context.Background(), 0, 40*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 30*time.Millisecond {
		t.Fatalf("observe returned too quickly after %s", elapsed)
	}
	if state.FrameRevision != 0 || image != nil {
		t.Fatalf("state=%+v image_bytes=%d", state, len(image))
	}
}

func TestObserveIgnoresNonFramebufferChanges(t *testing.T) {
	session := &Session{
		connected:     true,
		frameRevision: 7,
		notify:        make(chan struct{}),
	}
	result := make(chan State, 1)
	go func() {
		state, _, _ := session.Observe(context.Background(), 7, 80*time.Millisecond)
		result <- state
	}()

	time.Sleep(10 * time.Millisecond)
	session.mu.Lock()
	session.power = "on"
	session.signalChangeLocked()
	session.mu.Unlock()

	select {
	case state := <-result:
		t.Fatalf("observe returned for non-frame revision: %+v", state)
	case <-time.After(25 * time.Millisecond):
	}

	session.mu.Lock()
	session.frameRevision = 8
	session.signalChangeLocked()
	session.mu.Unlock()
	select {
	case state := <-result:
		if state.FrameRevision != 8 {
			t.Fatalf("frame revision=%d want=8", state.FrameRevision)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("observe did not return for a newer framebuffer revision")
	}
}

func TestObserveHonorsContextCancellation(t *testing.T) {
	session := &Session{connected: true, notify: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := session.Observe(ctx, 0, MaximumObserveWait)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context.Canceled", err)
	}
}
