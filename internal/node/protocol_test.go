package node

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtocolMismatchOnlyIdentifiesRejectedRequestShape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		message string
		want    bool
	}{
		{"legacy decoder", 400, "invalid protocol or game identity", true},
		{"deployment failed", 503, "operation unavailable; see node journal", false},
		{"different bad request", 400, "invalid server state", false},
		{"identity changed", 409, "server identity or boot changed", false},
		{"wrong status", 503, "invalid protocol or game identity", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(Response{Error: tc.message})
			}))
			defer server.Close()
			_, err := CallURL(context.Background(), server.Client(), server.URL, Request{Action: "prepare", HostDelivery: true})
			if err == nil {
				t.Fatal("failed request accepted")
			}
			if errors.Is(err, ErrProtocolMismatch) != tc.want {
				t.Fatalf("protocol retry classification: %v", err)
			}
		})
	}
}
