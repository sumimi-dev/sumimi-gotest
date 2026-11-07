// Package load generates traffic against a running server.
//
// A **pacer** decides when each request is due; **workers** send them. The
// schedule is computed from the start — request *i* is due at
// `start + i/rate` — so the offered rate does not drift and does not depend
// on how fast the workers happen to be.
//
// **The garbage collector is turned off** for the run. A collection pause
// would disturb the pacing, and the offered rate is the thing being measured.
// The client is a separate process: its own memory does not matter.
package load

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// What to do.
type Options struct {
	// The URL every request goes to.
	URL string
	// The body of every request.
	Payload []byte
	// The rate to offer, in requests a second. Zero means "as fast as it can".
	Rate int
	// How long to run.
	Duration time.Duration
	// How many connections to spread the requests over.
	Connections int
}

// What happened.
type Result struct {
	// How many requests were sent.
	Offered int64 `json:"offered"`
	// How many the server took (200).
	Accepted int64 `json:"accepted"`
	// How many the server refused (503 — the admission gate said no).
	Refused int64 `json:"refused"`
	// How many went wrong in any other way.
	Other int64 `json:"other"`
	// How long it took.
	Seconds float64 `json:"seconds"`
	// The rate that was actually achieved, in requests a second.
	Achieved float64 `json:"achieved"`
	// A few of the errors, so a broken run is not a silent one.
	Errors []string `json:"errors,omitempty"`
}

// Run the load.
func Run(ctx context.Context, httpClient *http.Client, options Options) Result {
	// The collector stays out of the way of the schedule.
	previous := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previous)

	if options.Connections < 1 {
		options.Connections = 1
	}
	if options.Duration <= 0 {
		options.Duration = time.Second
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, options.Duration)
	defer cancel()

	// The tickets: the due time of every request, in order.
	tickets := make(chan time.Time, options.Connections*8+1024)

	var offered, accepted, refused, other atomic.Int64
	var failures []string
	var failuresLock sync.Mutex

	var workers sync.WaitGroup
	for worker := 0; worker < options.Connections; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for due := range tickets {
				if !due.IsZero() {
					if delay := time.Until(due); delay > 0 {
						time.Sleep(delay)
					}
				}
				offered.Add(1)
				status, err := send(ctx, httpClient, options.URL, options.Payload)
				switch {
				case err != nil:
					other.Add(1)
					failuresLock.Lock()
					if len(failures) < 10 {
						failures = append(failures, err.Error())
					}
					failuresLock.Unlock()
				case status == http.StatusOK:
					accepted.Add(1)
				case status == http.StatusServiceUnavailable:
					refused.Add(1)
				default:
					other.Add(1)
					failuresLock.Lock()
					if len(failures) < 10 {
						failures = append(failures, fmt.Sprintf("the server said %d", status))
					}
					failuresLock.Unlock()
				}
			}
		}()
	}

	// The pacer closes the tickets when it is done: no more tickets, so the
	// workers finish.
	pace(ctx, options, tickets)
	workers.Wait()

	seconds := time.Since(started).Seconds()
	sent := offered.Load()
	result := Result{
		Offered:  sent,
		Accepted: accepted.Load(),
		Refused:  refused.Load(),
		Other:    other.Load(),
		Seconds:  seconds,
	}
	if seconds > 0 {
		result.Achieved = float64(sent) / seconds
	}
	result.Errors = failures
	return result
}

// The pacer: hand out the tickets, one per request, at the right moment.
//
// One timer, reused: a timer per ticket would allocate a hundred thousand
// times a second.
func pace(ctx context.Context, options Options, tickets chan<- time.Time) {
	defer close(tickets)

	started := time.Now()
	if options.Rate <= 0 {
		// As fast as it can: every ticket is due immediately.
		for {
			select {
			case <-ctx.Done():
				return
			case tickets <- time.Time{}:
			}
		}
	}

	interval := time.Duration(float64(time.Second) / float64(options.Rate))
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	for index := 0; ; index++ {
		due := started.Add(time.Duration(index) * interval)
		if delay := time.Until(due); delay > 0 {
			timer.Reset(delay)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
		select {
		case <-ctx.Done():
			return
		case tickets <- due:
		}
	}
}

// One request.
func send(ctx context.Context, httpClient *http.Client, url string, payload []byte) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := httpClient.Do(request)
	if err != nil {
		return 0, err
	}
	// The body is not needed, but it has to be drained for the connection to
	// go back into the pool.
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	return response.StatusCode, nil
}
