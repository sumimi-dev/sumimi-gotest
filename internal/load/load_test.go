package load

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// The offered rate is **the thing being measured**: if the tool cannot offer
// the rate it says it offers, every number it produces is a lie.
func TestTheOfferedRateIsTheRate(t *testing.T) {
	var served atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result := Run(context.Background(), server.Client(), Options{
		URL:         server.URL,
		Payload:     []byte(`{"website":"x","payload":{"url":"/load"}}`),
		Rate:        1000,
		Duration:    2 * time.Second,
		Connections: 8,
	})

	// Two seconds at a thousand a second: two thousand requests, give or take
	// the one that was in flight when the clock ran out.
	if result.Offered < 1500 || result.Offered > 2500 {
		t.Fatalf("offered %d requests in 2 s at 1000/s", result.Offered)
	}
	if result.Accepted+result.Other != result.Offered {
		t.Fatalf(
			"the counts do not add up: %d accepted, %d other, %d offered",
			result.Accepted, result.Other, result.Offered,
		)
	}
	if result.Accepted < 1500 {
		t.Fatalf("the server only took %d of %d", result.Accepted, result.Offered)
	}
	if served.Load() != result.Offered {
		t.Fatalf("the server saw %d requests, the tool sent %d", served.Load(), result.Offered)
	}
}

// A refusal is a refusal: the server says no (503) and the tool counts it as
// a refusal, not as an error.
func TestARefusalIsCountedAsARefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	result := Run(context.Background(), server.Client(), Options{
		URL:         server.URL,
		Payload:     []byte(`{}`),
		Rate:        500,
		Duration:    time.Second,
		Connections: 4,
	})

	if result.Refused == 0 {
		t.Fatalf("the server refused everything and the tool counted none: %+v", result)
	}
	if result.Accepted != 0 {
		t.Fatalf("the server refused everything and the tool counted %d accepted", result.Accepted)
	}
	if result.Other != 0 {
		t.Fatalf("a refusal is not an error: %+v", result)
	}
}

// The run ends when it is told to, and not a second later.
func TestTheRunStopsWhenItIsTold(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	started := time.Now()
	Run(context.Background(), server.Client(), Options{
		URL:         server.URL,
		Payload:     []byte(`{}`),
		Rate:        100,
		Duration:    500 * time.Millisecond,
		Connections: 2,
	})
	elapsed := time.Since(started)
	if elapsed > 3*time.Second {
		t.Fatalf("the run took %s, not half a second", elapsed)
	}
}
