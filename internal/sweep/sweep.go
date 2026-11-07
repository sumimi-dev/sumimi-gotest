// Package sweep runs the matrix: a rate curve.
//
// The point of the matrix is **comparability**: the same tool, the same
// numbers, the same machine — so a change can be checked against a baseline.
//
// When a binary is given, every point of the curve gets a **fresh** server:
// the peak resident set is a high-water mark, so it only goes up, and a
// server that has already served a hundred thousand requests cannot answer
// "what did this rate cost". When there is no binary, the running server is
// measured as it is — and the report says that the peak is cumulative.
package sweep

import (
	"context"
	"net/http"
	"time"

	"github.com/sumimi-dev/sumimi-gotest/internal/client"
	"github.com/sumimi-dev/sumimi-gotest/internal/load"
	"github.com/sumimi-dev/sumimi-gotest/internal/machine"
	"github.com/sumimi-dev/sumimi-gotest/internal/report"
	"github.com/sumimi-dev/sumimi-gotest/internal/server"
)

// Options for the curve.
type Options struct {
	// The binary of the server to measure. Empty means "an already-running
	// server".
	Binary string
	// The server to measure when there is no binary.
	BaseURL string
	// The user to log in with.
	User string
	// The password.
	Password string
	// The engine's memory limit for the servers this starts.
	MemoryLimit string
	// The rate the gate admits for the servers this starts. Zero means "the
	// recommended one".
	AdmitRPS int
	// The rates to offer, in requests a second.
	Rates []int
	// How long each rate runs.
	Seconds int
	// How many connections.
	Connections int
	// The size of every payload, in bytes.
	PayloadBytes int
	// How long to let the machine settle between two points.
	//
	// **This matters.** The server before this one has just written tens of
	// thousands of events to the disk, and the kernel is still writing them
	// back. The next server then competes with that writeback, its writer
	// falls behind, its queue grows — and the peak it reports is the
	// measurement of the *previous* point, not of this one. On a small box
	// with a slow disk it is the difference between 139 MB and 419 MB.
	Settle time.Duration
	// The report to fill in.
	Report *report.Report
}

// Run the curve.
func Run(ctx context.Context, options Options) error {
	if options.Binary == "" {
		options.Report.Notes = "The server was **not** restarted between the points, so the " +
			"peak is a high-water mark over the whole run — not the cost of one point."
	}
	for _, rate := range options.Rates {
		point, err := measure(ctx, options, rate)
		if err != nil {
			return err
		}
		options.Report.Points = append(options.Report.Points, point)
		// Let the machine settle before the next point: the last server has
		// just written tens of thousands of events, and the kernel is still
		// writing them back.
		if options.Settle > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(options.Settle):
			}
		}
	}
	return nil
}

// One measurement: the load, and the server's own numbers afterwards.
func measure(ctx context.Context, options Options, rate int) (report.Point, error) {
	baseURL := options.BaseURL
	var started *server.Server

	if options.Binary != "" {
		var err error
		started, err = server.Start(ctx, server.Options{
			Binary:      options.Binary,
			Password:    options.Password,
			MemoryLimit: options.MemoryLimit,
			AdmitRPS:    options.AdmitRPS,
		})
		if err != nil {
			return report.Point{}, err
		}
		defer started.Stop()
		baseURL = started.BaseURL
	}

	api := client.New(baseURL)
	if err := api.Login(ctx, options.User, options.Password); err != nil {
		return report.Point{}, err
	}
	website, err := api.CreateWebsite(ctx, "Sweep")
	if err != nil {
		return report.Point{}, err
	}

	body := client.Payload(website, options.PayloadBytes)
	result := load.Run(ctx, HTTPClient(), load.Options{
		URL:         baseURL + "/api/send",
		Payload:     body,
		Rate:        rate,
		Duration:    time.Duration(options.Seconds) * time.Second,
		Connections: options.Connections,
	})

	after, err := api.Heartbeat(ctx)
	if err != nil {
		return report.Point{}, err
	}

	point := report.Point{
		Rate:         int64(rate),
		Offered:      result.Offered,
		Accepted:     result.Accepted,
		Refused:      result.Refused,
		Other:        result.Other,
		Seconds:      result.Seconds,
		Achieved:     result.Achieved,
		PayloadBytes: len(body),
	}
	point.PeakRSSBytes = after.PeakRSSBytes
	point.RSSBytes = after.RSSBytes
	point.AdmitRPS = after.AdmitRPS
	point.RefusedByTheServer = after.RefusedRate + after.RefusedInflight + after.RefusedBody

	// The kernel's own account of the peak, when this sweep started the
	// server: `ru_maxrss` is exact on every platform, and the parent is the
	// one that gets it.
	if started != nil {
		started.Stop()
		if peak := started.PeakBytes(); peak > 0 {
			point.PeakRSSBytes = peak
		}
	}

	// The server's own account of itself: the machine that was measured.
	if after.Machine != nil && options.Report.Server == nil {
		options.Report.Server = &machine.Facts{
			CPU:      after.Machine.CPU,
			Cores:    int(after.Machine.Cores),
			RAMBytes: int64(after.Machine.RAMBytes),
			OS:       after.Machine.OS,
			Arch:     after.Machine.Arch,
		}
	}
	if options.Report.ServerVersion == "" {
		options.Report.ServerVersion = after.Version
	}
	return point, nil
}

// HTTPClient: a client tuned for many connections.
func HTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        1024,
			MaxIdleConnsPerHost: 1024,
			IdleConnTimeout:     90 * time.Second,
			// The payloads are small and the answers are thrown away.
			DisableCompression: true,
			WriteBufferSize:    16 << 10,
			ReadBufferSize:     16 << 10,
		},
	}
}
