// Package axes measures the trade between the two engines.
//
// Four axes, and they have to be measured on the **same data**: the peak
// memory, the data on disk, the latency of a report over a long range, and
// the memory a report needs.
//
// The long range is the point. On the few seconds of data a load test leaves
// behind, both engines answer in single-digit milliseconds and the
// difference is worth nothing; over a year or three, it is two orders of
// magnitude. So the tool **seeds** a history first — the same binary that
// serves also seeds — and then asks the report endpoint for the ranges.
//
// Nothing here trusts the tool's own arithmetic: the peak comes from the
// server's own high-water mark, and the latencies from the clock around the
// HTTP call.
package axes

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sumimi-dev/sumimi-gotest/internal/client"
	"github.com/sumimi-dev/sumimi-gotest/internal/report"
	"github.com/sumimi-dev/sumimi-gotest/internal/server"
)

// Options for the measurement.
type Options struct {
	// The binary of the server to measure. Empty means "an already-running
	// server" — and then only one backend can be measured.
	Binary string
	// The backends to measure: "duckdb", "sqlite", or both.
	Backends []string
	// The server to measure when there is no binary.
	BaseURL string
	// The user to log in with.
	User string
	// The password.
	Password string
	// How many days of history to seed.
	Days int
	// Roughly how many visitors a day the seeded history has.
	Visitors int
	// The ranges to measure, as fractions of the history: 0.17 is about six
	// months of a three-year history, 1 is all of it.
	Fractions []float64
	// The engine's memory limit for the servers this starts.
	MemoryLimit string
	// The environment for the servers this starts.
	Environment [][2]string
	// The report to fill in.
	Report *report.AxesReport
}

// Run the measurement, one backend at a time.
func Run(ctx context.Context, options Options) error {
	for _, backend := range options.Backends {
		measured, err := measure(ctx, options, backend)
		if err != nil {
			return fmt.Errorf("the %s backend: %w", backend, err)
		}
		options.Report.Backends = append(options.Report.Backends, measured)
	}
	return nil
}

// measure: one backend, from an empty directory to a table row.
func measure(ctx context.Context, options Options, backend string) (report.AxisBackend, error) {
	measured := report.AxisBackend{Backend: backend}

	// The server this measures.
	baseURL := options.BaseURL
	var started *server.Server
	var directory string

	if options.Binary != "" {
		var err error
		directory, err = seed(ctx, options, backend)
		if err != nil {
			return measured, err
		}
		started, err = server.Start(ctx, server.Options{
			Binary:      options.Binary,
			Password:    options.Password,
			MemoryLimit: options.MemoryLimit,
			Environment: options.Environment,
			Extra:       []string{"--backend", backend},
			Directory:   directory,
		})
		if err != nil {
			os.RemoveAll(directory)
			return measured, err
		}
		defer started.Stop()
		baseURL = started.BaseURL
	} else if len(options.Backends) > 1 {
		return measured, fmt.Errorf("an already-running server can only be one backend")
	}

	api := client.New(baseURL)
	if err := api.Login(ctx, options.User, options.Password); err != nil {
		return measured, err
	}

	// The website the history belongs to: the seed made one, and the tool
	// does not have to know its id.
	websites, err := api.Websites(ctx)
	if err != nil {
		return measured, err
	}
	if len(websites) == 0 {
		return measured, fmt.Errorf("the server has no website")
	}
	website := websites[0].ID

	// The reports: the first call for a range is the slow one, because the
	// report cache is cold. The ranges are measured in the order given, and
	// each one is a different range, so each first call is a miss.
	for _, fraction := range options.Fractions {
		days := int(float64(options.Days) * fraction)
		if days < 1 {
			days = 1
		}
		end := time.Now()
		start := end.AddDate(0, 0, -days)
		elapsed, err := api.Stats(ctx, website, start.UnixMilli(), end.UnixMilli())
		if err != nil {
			return measured, err
		}
		measured.Reports = append(measured.Reports, report.AxisPoint{
			Days:         days,
			Milliseconds: elapsed.Milliseconds(),
		})
	}

	// The server's own account of itself.
	heartbeat, err := api.Heartbeat(ctx)
	if err != nil {
		return measured, err
	}
	measured.PeakRSSBytes = heartbeat.PeakRSSBytes
	measured.RSSBytes = heartbeat.RSSBytes
	measured.EngineBytes = heartbeat.Memory
	measured.DatabaseBytes = uint64(heartbeat.Database)

	// The data on disk: the database file and the write-ahead log next to
	// it. The heartbeat counts the database; the log is the other half of
	// what the history costs.
	//
	// **Before the server stops** — stopping it removes the directory.
	if directory != "" {
		measured.DataBytes = dataBytes(directory)
	}
	if started != nil {
		started.Stop()
		if measured.PeakRSSBytes == 0 {
			measured.PeakRSSBytes = started.PeakBytes()
			options.Report.Notes = "The peak is the kernel's `ru_maxrss` — the server cannot " +
				"report its own high-water mark on this platform."
		}
		os.RemoveAll(directory)
	}
	return measured, nil
}

// seed: fill a directory with the history to measure.
//
// The binary that serves also seeds — `sumimi seed` — so the history is the
// same shape on every machine, and the tool needs nothing else.
func seed(ctx context.Context, options Options, backend string) (string, error) {
	directory, err := os.MkdirTemp("", "sumimi-gotest-axes-")
	if err != nil {
		return "", err
	}
	arguments := []string{
		"seed",
		"--dir", directory,
		"--days", strconv.Itoa(options.Days),
		"--visitors", strconv.Itoa(options.Visitors),
		"--backend", backend,
	}
	command := exec.CommandContext(ctx, options.Binary, arguments...)
	command.Env = append(os.Environ(),
		"SUMIMI_ADMIN_PASSWORD="+options.Password,
		"SUMIMI_JWT_SECRET=sumimi-gotest",
	)
	log, err := os.Create(filepath.Join(directory, "seed.log"))
	if err == nil {
		command.Stdout = log
		command.Stderr = log
	}
	if err := command.Run(); err != nil {
		os.RemoveAll(directory)
		message := ""
		if log != nil {
			if data, readErr := os.ReadFile(log.Name()); readErr == nil {
				message = ": " + string(data)
			}
			log.Close()
		}
		return "", fmt.Errorf("the seed failed: %w%s", err, message)
	}
	if log != nil {
		log.Close()
	}
	return directory, nil
}

// dataBytes: how much the history costs on disk.
//
// The database file and the write-ahead log beside it: the first is the
// history, the second is what the server has not written into it yet.
func dataBytes(directory string) uint64 {
	var total uint64
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name != "data.duckdb" && name != "data.duckdb.wal" &&
			name != "data.sqlite" && name != "data.sqlite-wal" {
			continue
		}
		if info, err := entry.Info(); err == nil {
			total += uint64(info.Size())
		}
	}
	return total
}
