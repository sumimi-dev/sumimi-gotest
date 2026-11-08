// Command sumimi-gotest measures a Sumimi server.
//
// Three commands:
//
//	load   — send traffic at a rate and count what came back
//	probe — what the server says about itself
//	sweep  — the matrix: a rate curve, written as a report
//
// The tool is written in Go and uses nothing but the standard library, so it
// is one static file that runs anywhere — on the machine being measured, or
// on another one, over the network.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sumimi-dev/sumimi-gotest/internal/client"
	"github.com/sumimi-dev/sumimi-gotest/internal/load"
	"github.com/sumimi-dev/sumimi-gotest/internal/report"
	"github.com/sumimi-dev/sumimi-gotest/internal/sweep"
)

// The version. The release workflow sets it with `-ldflags`.
var version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	command := os.Args[1]
	arguments := os.Args[2:]
	switch command {
	case "load":
		loadCommand(arguments)
	case "probe":
		probeCommand(arguments)
	case "sweep":
		sweepCommand(arguments)
	case "version", "--version":
		fmt.Println(version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", command)
		usage()
		os.Exit(2)
	}
}

// usage: what the tool does.
func usage() {
	fmt.Fprint(os.Stderr, `sumimi-gotest — measure a Sumimi server.

  load   --host 127.0.0.1 --port 3000 --rate 20000 --seconds 20
  probe  --host 127.0.0.1 --port 3000
  sweep  --host 127.0.0.1 --port 3000 --rates 5000,10000,20000 --out report.md

The password comes from SUMIMI_ADMIN_PASSWORD when the flag is not given.
`)
}

// serverFlags: the flags every command needs.
type serverFlags struct {
	host     *string
	port     *int
	user     *string
	password *string
}

// add: the flags every command needs.
func (s *serverFlags) add(flags *flag.FlagSet) {
	s.host = flags.String("host", "127.0.0.1", "the server's address")
	s.port = flags.Int("port", 3000, "the server's port")
	s.user = flags.String("user", "admin", "the user to log in with")
	s.password = flags.String("password", os.Getenv("SUMIMI_ADMIN_PASSWORD"), "the password")
}

// baseURL: where the server is.
func (s *serverFlags) baseURL() string {
	return fmt.Sprintf("http://%s:%d", *s.host, *s.port)
}

// loadCommand: send the traffic and count what came back.
func loadCommand(arguments []string) {
	flags := flag.NewFlagSet("load", flag.ExitOnError)
	var server serverFlags
	server.add(flags)
	rate := flags.Int("rate", 20000, "the rate to offer, in requests a second (0 = as fast as it can)")
	seconds := flags.Int("seconds", 20, "how long to run")
	connections := flags.Int("connections", 64, "how many connections to spread it over")
	payloadBytes := flags.Int("payload-bytes", 0, "the size of every payload, in bytes")
	out := flags.String("out", "", "write the JSON result to this file")
	_ = flags.Parse(arguments)

	ctx := context.Background()
	api := client.New(server.baseURL())
	if err := api.Login(ctx, *server.user, *server.password); err != nil {
		fail("could not log in: %v", err)
	}
	website, err := api.CreateWebsite(ctx, "Load")
	if err != nil {
		fail("could not create the website: %v", err)
	}

	body := client.Payload(website, *payloadBytes)
	before, _ := api.Heartbeat(ctx)

	result := load.Run(ctx, sweep.HTTPClient(), load.Options{
		URL:         server.baseURL() + "/api/send",
		Payload:     body,
		Rate:        *rate,
		Duration:    time.Duration(*seconds) * time.Second,
		Connections: *connections,
	})

	after, err := api.Heartbeat(ctx)
	if err != nil {
		fail("could not read the heartbeat: %v", err)
	}

	fmt.Printf("the rate:      %s/s offered, %s/s achieved\n", group(int64(*rate)), group(int64(result.Achieved+0.5)))
	fmt.Printf("the requests:  %s sent, %s taken, %s refused, %s other\n",
		group(result.Offered), group(result.Accepted), group(result.Refused), group(result.Other))
	fmt.Printf("the server:    admits %s/s, refused %s by its own counters\n",
		group(int64(after.AdmitRPS)), group(int64(after.RefusedRate+after.RefusedInflight+after.RefusedBody)))
	if after.PeakRSSBytes > 0 {
		fmt.Printf("the memory:    %s peak, %s now\n", size(after.PeakRSSBytes), size(after.RSSBytes))
	}
	if len(result.Errors) > 0 {
		fmt.Printf("the errors:    %s\n", strings.Join(result.Errors, "; "))
	}

	answer := map[string]any{
		"when":           time.Now().UTC().Format(time.RFC3339),
		"offeredRate":    *rate,
		"achievedRate":   result.Achieved,
		"seconds":        result.Seconds,
		"connections":    *connections,
		"payloadBytes":   len(body),
		"sent":           result.Offered,
		"accepted":       result.Accepted,
		"refused":        result.Refused,
		"other":          result.Other,
		"errors":         result.Errors,
		"serverVersion":  after.Version,
		"serverAdmitRps": after.AdmitRPS,
		"serverPeakRss":  after.PeakRSSBytes,
		"serverRss":      after.RSSBytes,
		"serverRefused":  after.RefusedRate + after.RefusedInflight + after.RefusedBody,
		"serverQueued":   after.Queued,
		"serverDropped":  after.Dropped,
		"beforeQueued":   before.Queued,
		"serverMachine":  after.Machine,
	}
	encoded, err := json.MarshalIndent(answer, "", "  ")
	if err != nil {
		fail("could not write the result: %v", err)
	}
	if *out == "" {
		fmt.Println(string(encoded))
		return
	}
	if err := os.WriteFile(*out, encoded, 0o644); err != nil {
		fail("could not write %s: %v", *out, err)
	}
	fmt.Printf("the result:     %s\n", *out)
}

// probeCommand: what the server says about itself.
func probeCommand(arguments []string) {
	flags := flag.NewFlagSet("probe", flag.ExitOnError)
	var server serverFlags
	server.add(flags)
	_ = flags.Parse(arguments)

	ctx := context.Background()
	api := client.New(server.baseURL())
	heartbeat, err := api.Heartbeat(ctx)
	if err != nil {
		fail("could not read the heartbeat: %v", err)
	}
	encoded, err := json.MarshalIndent(heartbeat, "", "  ")
	if err != nil {
		fail("could not write the answer: %v", err)
	}
	fmt.Println(string(encoded))
}

// sweepCommand: the matrix.
func sweepCommand(arguments []string) {
	flags := flag.NewFlagSet("sweep", flag.ExitOnError)
	var server serverFlags
	server.add(flags)
	binary := flags.String("binary", "", "the server binary to measure (a fresh one per point)")
	rates := flags.String("rates", "5000,10000,20000,40000", "the rates to offer, in requests a second")
	seconds := flags.Int("seconds", 20, "how long each rate runs")
	connections := flags.Int("connections", 64, "how many connections")
	payloadBytes := flags.Int("payload-bytes", 0, "the size of every payload, in bytes")
	memoryLimit := flags.String("memory-limit", "", "the engine's memory limit for the servers this starts")
	admitRPS := flags.Int("admit-rps", 0, "the rate the gate admits for the servers this starts")
	settle := flags.Int("settle", 5, "how long to let the machine settle between two points (seconds)")
	environments := flags.String("env", "", "an environment variable for the server, KEY=VALUE (repeatable, comma separated)")
	out := flags.String("out", "", "write the Markdown report to this file")
	jsonOut := flags.String("json", "", "write the JSON report to this file")
	_ = flags.Parse(arguments)

	parsed, err := parseRates(*rates)
	if err != nil {
		fail("%v", err)
	}

	document := report.New("sumimi-gotest", version)
	err = sweep.Run(context.Background(), sweep.Options{
		Environment:  splitEnvironment(*environments),
		Binary:       *binary,
		BaseURL:      server.baseURL(),
		User:         *server.user,
		Password:     *server.password,
		MemoryLimit:  *memoryLimit,
		AdmitRPS:     *admitRPS,
		Rates:        parsed,
		Seconds:      *seconds,
		Connections:  *connections,
		PayloadBytes: *payloadBytes,
		Settle:       time.Duration(*settle) * time.Second,
		Report:       &document,
	})
	if err != nil {
		fail("the sweep failed: %v", err)
	}

	markdown := document.Markdown()
	if *out == "" {
		fmt.Print(markdown)
	} else if err := os.WriteFile(*out, []byte(markdown), 0o644); err != nil {
		fail("could not write %s: %v", *out, err)
	} else {
		fmt.Printf("the report:     %s\n", *out)
	}
	if *jsonOut != "" {
		encoded, err := document.JSON()
		if err != nil {
			fail("could not write the report: %v", err)
		}
		if err := os.WriteFile(*jsonOut, encoded, 0o644); err != nil {
			fail("could not write %s: %v", *jsonOut, err)
		}
		fmt.Printf("the machine:    %s\n", *jsonOut)
	}
}

// parseRates: the rates of the curve.
func parseRates(text string) ([]int, error) {
	var rates []int
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		rate, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("the rate %q is not a number", part)
		}
		if rate < 0 {
			return nil, fmt.Errorf("the rate %d is not a rate", rate)
		}
		rates = append(rates, rate)
	}
	if len(rates) == 0 {
		return nil, fmt.Errorf("no rates to sweep")
	}
	return rates, nil
}

// fail: say what went wrong and stop.
func fail(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "sumimi-gotest: "+format+"\n", arguments...)
	os.Exit(1)
}

// group: a number with spaces between the thousands.
func group(number int64) string {
	text := fmt.Sprintf("%d", number)
	var parts []string
	for len(text) > 3 {
		parts = append([]string{text[len(text)-3:]}, parts...)
		text = text[:len(text)-3]
	}
	parts = append([]string{text}, parts...)
	return strings.Join(parts, " ")
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

// splitEnvironment: the `--env` flag's pairs.
func splitEnvironment(text string) [][2]string {
	var pairs [][2]string
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		pairs = append(pairs, [2]string{key, value})
	}
	return pairs
}
