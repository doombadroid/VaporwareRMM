package events

import (
	"sync"
	"testing"
	"time"
)

// TestPushOrDrop_DoesNotBlockWhenBufferFull asserts that a slow client
// (Send channel full and never drained) does not stall pushOrDrop. The
// non-blocking send + drop policy is the whole point of the
// backpressure rework — without it, one stuck dashboard could freeze
// the broadcast loop for everyone.
//
// We don't need a real websocket.Conn for this test; pushOrDrop only
// looks at info.Send. The previous WriteMessage-based fan-out had no
// drop policy at all and would block indefinitely on a slow peer.
func TestPushOrDrop_DoesNotBlockWhenBufferFull(t *testing.T) {
	info := &WSClientInfo{
		UserID:   "slow-reader",
		TenantID: "default",
		Role:     "admin",
		Send:     make(chan []byte, 2), // tiny buffer
	}

	// Fill the buffer.
	pushOrDrop(info, []byte("a"))
	pushOrDrop(info, []byte("b"))

	// One more push must drop, not block. We enforce that with a
	// completion deadline — if the push blocks even briefly the test
	// fails loud.
	done := make(chan struct{})
	go func() {
		pushOrDrop(info, []byte("c"))
		close(done)
	}()
	select {
	case <-done:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pushOrDrop blocked when buffer was full")
	}

	// Buffer still holds exactly the first two messages; the dropped
	// one is gone for good (that's the contract).
	if len(info.Send) != 2 {
		t.Errorf("Send len after drop: got %d, want 2", len(info.Send))
	}
}

// TestPushOrDrop_NoDataRace runs concurrent pushOrDrop callers against
// the same info struct to make sure the channel send is the synch
// boundary and we don't race on info.Send / info.UserID. Run with
// `go test -race` to actually catch that — the test suite enables it
// in CI.
func TestPushOrDrop_NoDataRace(t *testing.T) {
	info := &WSClientInfo{
		UserID:   "racer",
		TenantID: "default",
		Role:     "admin",
		Send:     make(chan []byte, 16),
	}
	// Drain to keep slack in the buffer so we exercise the success
	// path most of the time, but occasionally drop too.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-info.Send:
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				pushOrDrop(info, []byte("m"))
			}
		}()
	}
	wg.Wait()
	close(stop)
}

// TestPushOrDrop_NilInfoIsSafe is a defensive check: pushOrDrop must
// never panic on a nil info or a nil Send channel. Both can happen
// during the rollout window where a connection might be in WSClients
// without yet having a Send chan attached.
func TestPushOrDrop_NilInfoIsSafe(t *testing.T) {
	pushOrDrop(nil, []byte("x"))
	pushOrDrop(&WSClientInfo{}, []byte("x")) // Send is nil
}
