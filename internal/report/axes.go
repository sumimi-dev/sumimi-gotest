package report

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sumimi-dev/sumimi-gotest/internal/machine"
)

// AxesReport: the trade between the engines, on the same data.
//
// It is a different shape from the rate curve — a table of axes with one
// column per engine — so it is a different document: the same package, a
// different table.
type AxesReport struct {
	// The tool that made it.
	Tool string `json:"tool"`
	// Its version.
	Version string `json:"version"`
	// When it was made.
	When string `json:"when"`
	// The machine that **measured**.
	Client machine.Facts `json:"client"`
	// The machine that was **measured** — the server's own account of itself.
	Server *machine.Facts `json:"server,omitempty"`
	// The server's version.
	ServerVersion string `json:"serverVersion,omitempty"`
	// The history the axes were measured on.
	Days int `json:"days"`
	// Roughly how many visitors a day that history had.
	Visitors int `json:"visitors"`
	// One column per engine.
	Backends []AxisBackend `json:"backends"`
	// The charts — the same numbers, drawn.
	Charts []Chart `json:"charts,omitempty"`
	// Anything worth saying that the numbers do not say.
	Notes string `json:"notes,omitempty"`
}

// AxisBackend: one engine's numbers.
type AxisBackend struct {
	// The engine: "duckdb" or "sqlite".
	Backend string `json:"backend"`
	// The server's peak resident set, in bytes — the exact high-water mark
	// on Linux, the server's own account of itself.
	PeakRSSBytes uint64 `json:"peakRssBytes,omitempty"`
	// The resident set at the end.
	RSSBytes uint64 `json:"rssBytes,omitempty"`
	// What the engine says it holds.
	EngineBytes int64 `json:"engineBytes,omitempty"`
	// The data on disk: the database file and the write-ahead log.
	DataBytes uint64 `json:"dataBytes,omitempty"`
	// The database file alone.
	DatabaseBytes uint64 `json:"databaseBytes,omitempty"`
	// The reports, over the ranges.
	Reports []AxisPoint `json:"reports"`
}

// Chart: one picture — the URL of the SVG and the URL of the explorer.
//
// The data lives in the URL, so the chart is a link: it renders in an issue,
// in a release note and in a repository, with nothing installed. The SVG
// carries the numbers in a `<metadata>` element, so they can be read back
// out of the picture.
type Chart struct {
	// What the chart shows.
	Title string `json:"title"`
	// The picture.
	Picture string `json:"picture"`
	// The explorer: the same chart, where the numbers can be hovered.
	Explorer string `json:"explorer"`
}

// AxisPoint: one report, over one range.
type AxisPoint struct {
	// The length of the range, in days.
	Days int `json:"days"`
	// How long the **first** call for that range took — the cache was cold.
	Milliseconds int64 `json:"milliseconds"`
}

// NewAxes: an empty axes report, stamped with the moment and the machine.
func NewAxes(tool string, version string, days int, visitors int) AxesReport {
	return AxesReport{
		Tool:     tool,
		Version:  version,
		When:     time.Now().UTC().Format(time.RFC3339),
		Client:   machine.Current(),
		Days:     days,
		Visitors: visitors,
	}
}

// Markdown: the table, for the repository and for an issue.
func (a AxesReport) Markdown() string {
	var out strings.Builder
	out.WriteString("# The engines, four axes\n\n")
	out.WriteString(fmt.Sprintf("**The history**: %d days, about %d visitors a day.\n\n",
		a.Days, a.Visitors))
	out.WriteString(fmt.Sprintf("**The machine**: %s.\n\n", a.Client.String()))

	if len(a.Backends) == 0 {
		return out.String()
	}

	// The header: one column per engine.
	out.WriteString("| |")
	for _, backend := range a.Backends {
		out.WriteString(" **" + backend.Backend + "** |")
	}
	out.WriteString("\n| --- |")
	for range a.Backends {
		out.WriteString(" ---: |")
	}
	out.WriteString("\n")

	// The rows.
	out.WriteString("| the peak memory |")
	for _, backend := range a.Backends {
		out.WriteString(" " + size(backend.PeakRSSBytes) + " |")
	}
	out.WriteString("\n| the data on disk |")
	for _, backend := range a.Backends {
		out.WriteString(" " + size(backend.DataBytes) + " |")
	}
	out.WriteString("\n| what the engine holds |")
	for _, backend := range a.Backends {
		out.WriteString(" " + size(uint64(backend.EngineBytes)) + " |")
	}
	out.WriteString("\n")

	// The reports: one row per range.
	ranges := 0
	for _, backend := range a.Backends {
		if len(backend.Reports) > ranges {
			ranges = len(backend.Reports)
		}
	}
	for index := 0; index < ranges; index++ {
		label := ""
		for _, backend := range a.Backends {
			if index < len(backend.Reports) {
				label = fmt.Sprintf("the report over %d days", backend.Reports[index].Days)
				break
			}
		}
		out.WriteString("| " + label + " |")
		for _, backend := range a.Backends {
			if index < len(backend.Reports) {
				out.WriteString(fmt.Sprintf(" %.1f ms |",
					float64(backend.Reports[index].Milliseconds)))
			} else {
				out.WriteString(" — |")
			}
		}
		out.WriteString("\n")
	}

	// The charts: the same numbers, drawn.
	for _, chart := range a.Charts {
		out.WriteString("\n**" + chart.Title + "**\n\n")
		out.WriteString(fmt.Sprintf("[![%s](%s)](%s)\n", chart.Title, chart.Picture, chart.Explorer))
	}

	if a.Notes != "" {
		out.WriteString("\n" + a.Notes + "\n")
	}
	return out.String()
}

// JSON: the same numbers, for the next tool to read.
func (a AxesReport) JSON() ([]byte, error) {
	return json.MarshalIndent(a, "", "  ")
}
