package egg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/edgegamers/source-egg/internal/game"
	"github.com/edgegamers/source-egg/internal/node"
)

func TestConsoleDeliveryIsBoundedAndCancellationStopsSlowNode(t *testing.T) {
	entered := make(chan struct{}, 1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req node.Request
		json.NewDecoder(r.Body).Decode(&req)
		if req.Action != "logs" || len(req.Console) > 32 {
			t.Error("invalid log delivery")
		}
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer h.Close()
	p, _ := game.Lookup("css")
	id, _ := p.Identity("public")
	egg := &Egg{Node: h.Client(), NodeEndpoint: h.URL}
	delivery := newConsoleDelivery(egg, id, nil)
	for range 10000 {
		delivery.offer("fixture")
	}
	if delivery.dropped.Load() == 0 {
		t.Fatal("unavailable node queue grew without bound")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { delivery.run(ctx); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("no node delivery")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("node cancellation stalled console shutdown")
	}
}
