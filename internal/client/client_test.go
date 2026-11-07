package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A server that answers like Sumimi does.
func fakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "a-token"})
	})
	mux.HandleFunc("/api/websites", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer a-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": "website-1"})
	})
	mux.HandleFunc("/api/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":           true,
			"version":      "0.4.0",
			"admitRps":     20000,
			"rssBytes":     12345678,
			"peakRssBytes": 23456789,
			"machine": map[string]any{
				"os":    "linux",
				"arch":  "x86_64",
				"cores": 2,
			},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// The three calls the tool makes: log in, make a website, read the heartbeat.
func TestTheThreeCalls(t *testing.T) {
	server := fakeServer(t)
	api := New(server.URL)

	if err := api.Login(context.Background(), "admin", "secret"); err != nil {
		t.Fatalf("could not log in: %v", err)
	}
	if api.Token() != "a-token" {
		t.Fatalf("the token is %q", api.Token())
	}

	website, err := api.CreateWebsite(context.Background(), "Load")
	if err != nil {
		t.Fatalf("could not create the website: %v", err)
	}
	if website != "website-1" {
		t.Fatalf("the website is %q", website)
	}

	heartbeat, err := api.Heartbeat(context.Background())
	if err != nil {
		t.Fatalf("could not read the heartbeat: %v", err)
	}
	if heartbeat.Version != "0.4.0" {
		t.Fatalf("the version is %q", heartbeat.Version)
	}
	if heartbeat.AdmitRPS != 20000 {
		t.Fatalf("the rate is %d", heartbeat.AdmitRPS)
	}
	if heartbeat.PeakRSSBytes != 23456789 {
		t.Fatalf("the peak is %d", heartbeat.PeakRSSBytes)
	}
	if heartbeat.Machine == nil || heartbeat.Machine.OS != "linux" {
		t.Fatalf("the machine is %+v", heartbeat.Machine)
	}
}

// A website cannot be made without a token.
func TestAWebsiteNeedsAToken(t *testing.T) {
	server := fakeServer(t)
	api := New(server.URL)
	if _, err := api.CreateWebsite(context.Background(), "Load"); err == nil {
		t.Fatal("a website was made without logging in")
	}
}

// The payload is the size it was asked for.
//
// A payload cannot be smaller than the body itself — the request has to carry
// the website and the event — so the small sizes get the smallest body and
// the rest are exact.
func TestThePayloadIsTheSizeItShouldBe(t *testing.T) {
	small := Payload("website-1", 0)
	if len(small) == 0 {
		t.Fatal("the payload is empty")
	}
	for _, size := range []int{256, 1024, 10 * 1024} {
		body := Payload("website-1", size)
		if len(body) != size {
			t.Fatalf("the payload is %d bytes, not %d", len(body), size)
		}
	}
}
