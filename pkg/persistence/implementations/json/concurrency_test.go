package json

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ottermq/ottermq/pkg/persistence"
)

// Regression test for #236: SaveMessage and DeleteMessage on the same queue
// from different goroutines (publish and ack) must neither corrupt the file
// nor lose updates.
func TestConcurrentSaveAndDeleteMessage(t *testing.T) {
	p, err := NewJsonPersistence(&persistence.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewJsonPersistence: %v", err)
	}
	if err := p.SaveQueueMetadata("/", "q", persistence.QueueProperties{Durable: true}); err != nil {
		t.Fatalf("SaveQueueMetadata: %v", err)
	}
	props := persistence.MessageProperties{DeliveryMode: 2}
	const n = 200
	for i := range n {
		if err := p.SaveMessage("/", "q", fmt.Sprintf("old-%d", i), []byte("x"), props); err != nil {
			t.Fatalf("SaveMessage old-%d: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range n {
			if err := p.SaveMessage("/", "q", fmt.Sprintf("new-%d", i), []byte("x"), props); err != nil {
				errs <- fmt.Errorf("SaveMessage new-%d: %w", i, err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := range n {
			if err := p.DeleteMessage("/", "q", fmt.Sprintf("old-%d", i)); err != nil {
				errs <- fmt.Errorf("DeleteMessage old-%d: %w", i, err)
			}
		}
	}()
	wg.Wait()
	close(errs)
	nErrs := 0
	for err := range errs {
		if nErrs < 3 {
			t.Error(err)
		}
		nErrs++
	}
	if nErrs > 0 {
		t.Errorf("%d persistence calls failed", nErrs)
	}

	msgs, err := p.LoadMessages("/", "q")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	seen := make(map[string]bool, len(msgs))
	stale := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.ID, "old-") {
			stale++
		}
		seen[m.ID] = true
	}
	lost := 0
	for i := range n {
		if !seen[fmt.Sprintf("new-%d", i)] {
			lost++
		}
	}
	if stale > 0 || lost > 0 || len(msgs) != n {
		t.Errorf("persisted %d messages, want %d: %d deleted messages came back, %d new messages lost",
			len(msgs), n, stale, lost)
	}
}

// Bindings are a load-modify-save of the exchange file as well; concurrent
// binds to one exchange must all be kept.
func TestConcurrentSaveBindingState(t *testing.T) {
	p, err := NewJsonPersistence(&persistence.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewJsonPersistence: %v", err)
	}
	if err := p.SaveExchangeMetadata("/", "ex", "direct", persistence.ExchangeProperties{Durable: true}); err != nil {
		t.Fatalf("SaveExchangeMetadata: %v", err)
	}

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			if err := p.SaveBindingState("/", "ex", fmt.Sprintf("q-%d", i), "rk", nil); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("SaveBindingState: %v", err)
	}

	bindings, err := p.LoadExchangeBindings("/", "ex")
	if err != nil {
		t.Fatalf("LoadExchangeBindings: %v", err)
	}
	if len(bindings) != n {
		t.Errorf("persisted %d bindings, want %d", len(bindings), n)
	}
}
