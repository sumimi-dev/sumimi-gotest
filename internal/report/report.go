// Package report turns a measurement into something a person can read.
//
// Two forms: Markdown (for the repository, for an issue) and JSON (for the
// next tool to read). Both carry the machine's identity — a number without
// the machine it belongs to is not a measurement.
package report

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sumimi-dev/sumimi-gotest/internal/machine"
)

// One measurement.
type Point struct {
	// The rate that was offered, in requests a second.
	Rate int64 `json:"rate"`
	// How many requests were sent.
	Offered int64 `json:"offered"`
	// How many the server took.
	Accepted int64 `json:"accepted"`
	// How many the server refused.
	Refused int64 `json:"refused"`
	// How many went wrong in another way.
	Other int64 `json:"other"`
	// How long it took, in seconds.
	Seconds float64 `json:"seconds"`
	// The rate that was actually achieved, in requests a second.
	Achieved float64 `json:"achieved"`
	// The server's peak resident set, in bytes — the number the memory
	// budgets are about.
	PeakRSSBytes uint64 `json:"peakRssBytes,omitempty"`
	// The server's resident set at the end of the run.
	RSSBytes uint64 `json:"rssBytes,omitempty"`
	// The rate the server admits.
	AdmitRPS uint64 `json:"admitRps,omitempty"`
	// How many requests the server says it refused.
	RefusedByTheServer uint64 `json:"refusedByTheServer,omitempty"`
	// How many events were waiting to be written when the run ended — the
	// backlog is where the memory goes when the writer cannot keep up.
	Queued uint64 `json:"queued,omitempty"`
	// How many events the server had to drop.
	Dropped uint64 `json:"dropped,omitempty"`
	// What the engine was holding, in bytes.
	EngineBytes int64 `json:"engineBytes,omitempty"`
	// The 99th percentile of the write durations, in milliseconds.
	FlushP99MS uint64 `json:"flushP99Ms,omitempty"`
	// The slowest write, in milliseconds.
	FlushMaxMS uint64 `json:"flushMaxMs,omitempty"`
	// The size of every payload.
	PayloadBytes int `json:"payloadBytes,omitempty"`
}

// The report.
type Report struct {
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
	// The measurements.
	Points []Point `json:"points"`
	// Anything worth saying that the numbers do not say.
	Notes string `json:"notes,omitempty"`
}

// New: an empty report, stamped with the moment and the machine.
func New(tool string, version string) Report {
	return Report{
		Tool:    tool,
		Version: version,
		When:    time.Now().UTC().Format("2006-01-02 15:04 UTC"),
		Client:  machine.Current(),
	}
}

// Markdown: the report, for a person.
func (r Report) Markdown() string {
	var out strings.Builder

	fmt.Fprintf(&out, "# The measurement — %s\n\n", r.When)
	fmt.Fprintf(&out, "**The machine that was measured**: %s\n", serverMachine(r))
	fmt.Fprintf(&out, "**The machine that measured it**: %s\n", r.Client.String())
	if r.ServerVersion != "" {
		fmt.Fprintf(&out, "**The server**: Sumimi %s\n", r.ServerVersion)
	}
	out.WriteString("\n")

	out.WriteString("| the rate offered | sent | accepted | refused | the rate achieved | the server's peak |\n")
	out.WriteString("| ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, point := range r.Points {
		fmt.Fprintf(
			&out,
			"| %s | %s | %s | %s | %s | %s |\n",
			group(point.Rate),
			group(point.Offered),
			group(point.Accepted),
			group(point.Refused),
			fmt.Sprintf("%s/s", group(int64(point.Achieved+0.5))),
			peak(point.PeakRSSBytes),
		)
	}

	// The write path's own account: where the memory goes when the machine
	// cannot keep up.
	out.WriteString("\n| the rate offered | the queue at the end | the engine held | the slowest write |\n")
	out.WriteString("| ---: | ---: | ---: | ---: |\n")
	for _, point := range r.Points {
		fmt.Fprintf(
			&out,
			"| %s | %s | %s | %s |\n",
			group(point.Rate),
			group(int64(point.Queued)),
			size(uint64(point.EngineBytes)),
			fmt.Sprintf("%d ms", point.FlushMaxMS),
		)
	}

	if r.Notes != "" {
		fmt.Fprintf(&out, "\n%s\n", r.Notes)
	}
	return out.String()
}

// JSON: the report, for a machine.
func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// serverMachine: the machine that was measured.
func serverMachine(r Report) string {
	if r.Server == nil {
		return "unknown (the server did not say)"
	}
	return r.Server.String()
}

// group: a number with spaces between the thousands.
func group(number int64) string {
	text := fmt.Sprintf("%d", number)
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign = "-"
		text = text[1:]
	}
	var parts []string
	for len(text) > 3 {
		parts = append([]string{text[len(text)-3:]}, parts...)
		text = text[:len(text)-3]
	}
	parts = append([]string{text}, parts...)
	return sign + strings.Join(parts, " ")
}

// size: a number of bytes, in the biggest unit that fits.
func size(bytes uint64) string {
	const unit = 1024 * 1024
	if bytes < unit {
		return fmt.Sprintf("%d KB", bytes/1024)
	}
	megabytes := bytes / unit
	if megabytes < 1024 {
		return fmt.Sprintf("%d MB", megabytes)
	}
	return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
}

// peak: the peak, or the truth — that it was not reported.
//
// The exact high-water mark comes from the kernel, and only some platforms
// hand it over. "0 KB" would be a lie; "not reported" is not.
func peak(bytes uint64) string {
	if bytes == 0 {
		return "not reported"
	}
	return size(bytes)
}
