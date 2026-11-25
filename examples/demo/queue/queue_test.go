package queue

import (
	"math/rand/v2"
	"testing"
	"time"
)

// A worker with variable latency and a hard-coded 3ms wait: the classic timing flake.
func drain(n int) <-chan int {
	ch := make(chan int)
	go func() {
		for i := 0; i < n; i++ {
			time.Sleep(time.Duration(rand.IntN(1500)) * time.Microsecond)
		}
		ch <- n
	}()
	return ch
}

func TestDrain(t *testing.T) {
	select {
	case <-drain(3):
	case <-time.After(7 * time.Millisecond):
		t.Fatal("timed out waiting for queue to drain after 7ms")
	}
}

func TestDrainLarge(t *testing.T) {
	select {
	case <-drain(2):
	case <-time.After(4 * time.Millisecond):
		t.Fatalf("timed out waiting for queue to drain after %dms", 4)
	}
}

func TestEnqueueOrder(t *testing.T) {
	for i := 0; i < 5; i++ {
		time.Sleep(time.Millisecond)
	}
}

func TestSizeLimit(t *testing.T)    {}
func TestEmptyPop(t *testing.T)     {}
func TestPriorityLane(t *testing.T) {}
