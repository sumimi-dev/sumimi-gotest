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
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sumimi-dev/sumimi-gotest/internal/client"
	"github.com/sumimi-dev/sumimi-gotest/internal/draw"
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
	// The engine limits to measure, e.g. "512MB,256MB,64MB". Empty means
	// "not this time".
	//
	// **The limit is not a test of whether the report fits** — DuckDB spills
	// to disk and answers anyway. It is a test of what the limit costs: the
	// time, and the disk.
	Limits []string
	// The engine's memory limit for the servers this starts.
	MemoryLimit string
	// The environment for the servers this starts.
	Environment [][2]string
	// The report to fill in.
	Report *report.AxesReport
}

// Run the measurement, one backend at a time.
func Run(ctx context.Context, options Options) error {
	// The DuckDB directory is kept for the limits: the limits are measured on
	// the same history, so the same seed.
	var duckDirectory string
	for _, backend := range options.Backends {
		measured, directory, err := measure(ctx, options, backend)
		if err != nil {
			return fmt.Errorf("the %s backend: %w", backend, err)
		}
		options.Report.Backends = append(options.Report.Backends, measured)
		if backend == "duckdb" && directory != "" {
			duckDirectory = directory
		} else if directory != "" {
			os.RemoveAll(directory)
		}
	}
	if len(options.Limits) > 0 && duckDirectory != "" {
		limits, err := measureLimits(ctx, options, duckDirectory)
		if err != nil {
			return err
		}
		options.Report.Limits = limits
	}
	options.Report.Charts = charts(options.Report.Backends)
	options.Report.Charts = append(options.Report.Charts, limitCharts(options.Report.Limits)...)
	return nil
}

// charts: the same numbers, drawn.
//
// **Two mixed charts**, because the axes have three units and a line on the
// bars' scale would be a lie:
//
//  1. the four axes — the bars are the sizes (MB), the line is the latency
//     (ms) on its own axis;
//  2. the report over a range — the bars are the two engines, the line is how
//     many times slower the other one is.
func charts(backends []report.AxisBackend) []report.Chart {
	if len(backends) == 0 {
		return nil
	}
	var out []report.Chart

	// The labels: the engines.
	labels := make([]string, 0, len(backends))
	for _, backend := range backends {
		labels = append(labels, backend.Backend)
	}

	// The four axes: the sizes as bars, the latency as the line.
	memory := make([]float64, 0, len(backends))
	disk := make([]float64, 0, len(backends))
	latency := make([]float64, 0, len(backends))
	for _, backend := range backends {
		memory = append(memory, megabytes(backend.PeakRSSBytes))
		disk = append(disk, megabytes(backend.DataBytes))
		longest := int64(0)
		for _, report := range backend.Reports {
			if report.Milliseconds > longest {
				longest = report.Milliseconds
			}
		}
		latency = append(latency, float64(longest))
	}
	picture, explorer := draw.Combo(
		"The four axes",
		"MB",
		labels,
		[]draw.Series{
			{Name: "the peak memory", Values: memory},
			{Name: "the data on disk", Values: disk},
		},
		draw.Series{Name: "the longest report (ms)", Values: latency},
	)
	out = append(out, report.Chart{
		Title:    "The four axes — the sizes as bars, the latency as the line",
		Picture:  picture,
		Explorer: explorer,
	})

	// The report over a range: the two engines' bars, and the ratio.
	ranges := 0
	for _, backend := range backends {
		if len(backend.Reports) > ranges {
			ranges = len(backend.Reports)
		}
	}
	if ranges > 0 {
		rangeLabels := make([]string, 0, ranges)
		for index := 0; index < ranges; index++ {
			rangeLabels = append(rangeLabels, "")
		}
		series := make([]draw.Series, 0, len(backends))
		ratio := make([]float64, 0, ranges)
		for _, backend := range backends {
			values := make([]float64, 0, ranges)
			for index := 0; index < ranges; index++ {
				if index < len(backend.Reports) {
					values = append(values, float64(backend.Reports[index].Milliseconds))
					rangeLabels[index] = fmt.Sprintf("%d days", backend.Reports[index].Days)
				} else {
					values = append(values, 0)
				}
			}
			series = append(series, draw.Series{Name: backend.Backend, Values: values})
		}
		// The ratio: the first engine against the fastest one.
		for index := 0; index < ranges; index++ {
			fastest := 0.0
			for _, one := range series {
				if one.Values[index] > 0 && (fastest == 0 || one.Values[index] < fastest) {
					fastest = one.Values[index]
				}
			}
			slowest := 0.0
			for _, one := range series {
				if one.Values[index] > slowest {
					slowest = one.Values[index]
				}
			}
			if fastest > 0 {
				ratio = append(ratio, math.Round(slowest/fastest))
			} else {
				ratio = append(ratio, 0)
			}
		}
		picture, explorer = draw.Combo(
			"The report over a range",
			"ms",
			rangeLabels,
			series,
			draw.Series{Name: "how many times slower", Values: ratio},
		)
		out = append(out, report.Chart{
			Title:    "The report over a range — the bars are the engines, the line is the ratio",
			Picture:  picture,
			Explorer: explorer,
		})
	}

	return out
}

// megabytes: a number of bytes, in megabytes — rounded, because a chart
// shows "68" and not "68.25".
func megabytes(bytes uint64) float64 {
	value := float64(bytes) / (1024 * 1024)
	return math.Round(value*10) / 10
}

// measure: one backend, from an empty directory to a table row.
//
// It also returns the directory it seeded, so the limits can be measured on
// the same history — the caller removes it.
func measure(ctx context.Context, options Options, backend string) (report.AxisBackend, string, error) {
	measured := report.AxisBackend{Backend: backend}

	// The server this measures.
	baseURL := options.BaseURL
	var started *server.Server
	var directory string

	if options.Binary != "" {
		var err error
		directory, err = seed(ctx, options, backend)
		if err != nil {
			return measured, "", err
		}
		started, err = server.Start(ctx, server.Options{
			Binary:      options.Binary,
			Password:    options.Password,
			MemoryLimit: options.MemoryLimit,
			Environment: options.Environment,
			Extra:       []string{"--backend", backend},
			Directory:   directory,
			// The directory has to outlive the server: the limits are
			// measured on it.
			Keep: true,
		})
		if err != nil {
			os.RemoveAll(directory)
			return measured, "", err
		}
		defer started.Stop()
		baseURL = started.BaseURL
	} else if len(options.Backends) > 1 {
		return measured, "", fmt.Errorf("an already-running server can only be one backend")
	}

	api := client.New(baseURL)
	if err := api.Login(ctx, options.User, options.Password); err != nil {
		return measured, directory, err
	}

	// The website the history belongs to: the seed made one, and the tool
	// does not have to know its id.
	websites, err := api.Websites(ctx)
	if err != nil {
		return measured, directory, err
	}
	if len(websites) == 0 {
		return measured, directory, fmt.Errorf("the server has no website")
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
			return measured, directory, err
		}
		measured.Reports = append(measured.Reports, report.AxisPoint{
			Days:         days,
			Milliseconds: elapsed.Milliseconds(),
		})
	}

	// The server's own account of itself.
	heartbeat, err := api.Heartbeat(ctx)
	if err != nil {
		return measured, directory, err
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
	}
	return measured, directory, nil
}

// measureLimits: the same history, several engine limits.
//
// **The limit does not decide whether the report can be answered.** DuckDB
// spills to disk and answers anyway — what the limit decides is how long the
// answer takes and how much disk it costs on the way.
//
// So every limit gets its own server, on the same directory, and the same
// report. **The report cache is emptied before each one**: the cache lives in
// the instance directory, so it survives the server that filled it — and a
// second server would answer from it, which is the wrong number.
func measureLimits(ctx context.Context, options Options, directory string) ([]report.AxisLimit, error) {
	var out []report.AxisLimit
	for _, limit := range options.Limits {
		// The spill of the last limit, if the server was killed before it
		// could clean up.
		removeSpill(directory)

		started, err := server.Start(ctx, server.Options{
			Binary:      options.Binary,
			Password:    options.Password,
			MemoryLimit: limit,
			Environment: options.Environment,
			Extra:       []string{"--backend", "duckdb"},
			Directory:   directory,
			Keep:        true,
		})
		if err != nil {
			return out, err
		}

		api := client.New(started.BaseURL)
		if err := api.Login(ctx, options.User, options.Password); err != nil {
			started.Stop()
			return out, err
		}
		websites, err := api.Websites(ctx)
		if err != nil || len(websites) == 0 {
			started.Stop()
			if err == nil {
				err = fmt.Errorf("the server has no website")
			}
			return out, err
		}
		website := websites[0].ID

		// The cache: the first server's answers must not answer for this one.
		if err := api.PurgeCache(ctx); err != nil {
			started.Stop()
			return out, err
		}

		// The watcher: the most the engine ever spilled. The spill is
		// temporary — DuckDB deletes it when the query ends — so it has to be
		// watched, not looked at afterwards.
		stop := make(chan struct{})
		var watcher sync.WaitGroup
		var spilled uint64
		watcher.Add(1)
		go func() {
			defer watcher.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if size := spillBytes(directory); size > spilled {
					spilled = size
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()

		end := time.Now()
		start := end.AddDate(0, 0, -options.Days)
		elapsed, err := api.Stats(ctx, website, start.UnixMilli(), end.UnixMilli())
		close(stop)
		watcher.Wait()
		if err != nil {
			started.Stop()
			return out, err
		}

		// The peak: the server's own account of itself, or the kernel's.
		peak := uint64(0)
		if heartbeat, err := api.Heartbeat(ctx); err == nil {
			peak = heartbeat.PeakRSSBytes
		}
		started.Stop()
		if peak == 0 {
			peak = started.PeakBytes()
		}

		out = append(out, report.AxisLimit{
			Limit:        limit,
			Days:         options.Days,
			Milliseconds: elapsed.Milliseconds(),
			SpillBytes:   spilled,
			PeakRSSBytes: peak,
		})
	}
	return out, nil
}

// spillBytes: how much the engine spilled to disk.
//
// DuckDB's temporary directory sits next to the database file and is called
// `<file>.tmp` — but the name is DuckDB's business, not ours, so the tool
// looks for any directory that ends in `.tmp` and measures what is in it.
func spillBytes(directory string) uint64 {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0
	}
	var total uint64
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		total += treeBytes(filepath.Join(directory, entry.Name()))
	}
	return total
}

// treeBytes: the size of everything under a directory.
func treeBytes(root string) uint64 {
	var total uint64
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += uint64(info.Size())
		}
		return nil
	})
	return total
}

// removeSpill: the spill of an earlier run, if it is still there.
func removeSpill(directory string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".tmp") {
			_ = os.RemoveAll(filepath.Join(directory, entry.Name()))
		}
	}
}

// limitCharts: the limit's trade, drawn.
//
// **The bars are the sizes and the line is the time.** The units go in the
// series' names and not on the axis: the axis is shared, and one axis cannot
// be both megabytes and milliseconds.
func limitCharts(limits []report.AxisLimit) []report.Chart {
	if len(limits) == 0 {
		return nil
	}
	labels := make([]string, 0, len(limits))
	memory := make([]float64, 0, len(limits))
	spill := make([]float64, 0, len(limits))
	latency := make([]float64, 0, len(limits))
	for _, limit := range limits {
		labels = append(labels, limit.Limit)
		memory = append(memory, megabytes(limit.PeakRSSBytes))
		spill = append(spill, megabytes(limit.SpillBytes))
		latency = append(latency, float64(limit.Milliseconds))
	}
	picture, explorer := draw.Combo(
		"The limit trades memory for time and disk",
		"",
		labels,
		[]draw.Series{
			{Name: "the peak memory (MB)", Values: memory},
			{Name: "the spill on disk (MB)", Values: spill},
		},
		draw.Series{Name: "the report (ms)", Values: latency},
	)
	return []report.Chart{{
		Title:    "The limit's trade — the sizes as bars, the time as the line",
		Picture:  picture,
		Explorer: explorer,
	}}
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
