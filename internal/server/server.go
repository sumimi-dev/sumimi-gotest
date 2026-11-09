// Package server starts a Sumimi server — a **released** binary — and waits
// for it to answer.
//
// The measurement wants a **fresh** server for every point of the curve: the
// peak resident set is a high-water mark, so it only goes up, and a server
// that has already served a hundred thousand requests cannot answer "what did
// this rate cost".
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Options for a server.
type Options struct {
	// The binary to run — the released one, usually.
	Binary string
	// The port to bind to. Zero means "a free one".
	Port int
	// The admin's password.
	Password string
	// The engine's memory limit, e.g. "256MB".
	MemoryLimit string
	// The rate the gate admits. Zero means "the recommended one".
	AdmitRPS int
	// The instance directory to serve. Empty means "a fresh temporary one" —
	// which is what a rate curve wants, because every point of the curve
	// needs a server that has never served anything.
	//
	// A measurement that needs a **history** (the reports over a long range)
	// points this at a directory it has already filled.
	Directory string
	// Extra arguments for the server.
	Extra []string
	// Extra environment variables for the server.
	Environment [][2]string
	// Keep the instance directory when the server stops.
	//
	// **The default is to remove it** — a measurement that does not need the
	// files afterwards should not leave them behind. But a measurement that
	// starts *several* servers on the same history (the engine's limits) needs
	// the directory to outlive the first one.
	Keep bool
}

// A running server.
type Server struct {
	// The base URL to talk to.
	BaseURL string
	// The instance directory.
	Dir string
	// The port it listens on.
	Port int

	command *exec.Cmd
	log     *os.File
	peak    uint64
	stopped bool
	keep    bool
}

// Start the server and wait until it answers.
func Start(ctx context.Context, options Options) (*Server, error) {
	if options.Binary == "" {
		return nil, fmt.Errorf("no binary to run")
	}
	directory := options.Directory
	if directory == "" {
		created, err := os.MkdirTemp("", "sumimi-gotest-")
		if err != nil {
			return nil, err
		}
		directory = created
	}
	port := options.Port
	if port == 0 {
		free, err := FreePort()
		if err != nil {
			os.RemoveAll(directory)
			return nil, err
		}
		port = free
	}

	arguments := append([]string{"serve", "--dir", directory, "--port", strconv.Itoa(port)}, options.Extra...)
	command := exec.Command(options.Binary, arguments...)
	command.Env = append(
		os.Environ(),
		"SUMIMI_ADMIN_USER=admin",
		"SUMIMI_ADMIN_PASSWORD="+options.Password,
		"SUMIMI_JWT_SECRET=sumimi-gotest",
	)
	if options.MemoryLimit != "" {
		command.Env = append(command.Env, "SUMIMI_DUCKDB_MEMORY_LIMIT="+options.MemoryLimit)
	}
	if options.AdmitRPS > 0 {
		command.Env = append(command.Env, "SUMIMI_MAX_ADMIT_RPS="+strconv.Itoa(options.AdmitRPS))
	}
	for _, pair := range options.Environment {
		command.Env = append(command.Env, pair[0]+"="+pair[1])
	}

	// The server's own log: it is what a failed start has to be read from.
	log, err := os.Create(filepath.Join(directory, "server.log"))
	if err == nil {
		command.Stdout = log
		command.Stderr = log
	}
	if err := command.Start(); err != nil {
		os.RemoveAll(directory)
		return nil, err
	}

	server := &Server{
		BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		Dir:     directory,
		Port:    port,
		command: command,
		log:     log,
		keep:    options.Keep,
	}
	if err := server.wait(ctx, 90*time.Second); err != nil {
		server.Stop()
		return nil, err
	}
	return server, nil
}

// wait: until the heartbeat answers, or the time is up.
func (s *Server) wait(ctx context.Context, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/api/heartbeat", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("the server did not answer in %s\n%s", limit, s.Log())
}

// Log: what the server said on its way up.
func (s *Server) Log() string {
	if s.log == nil {
		return ""
	}
	data, err := os.ReadFile(s.log.Name())
	if err != nil {
		return ""
	}
	return string(data)
}

// Stop the server and clean up after it.
func (s *Server) Stop() {
	if s.stopped {
		return
	}
	s.stopped = true
	if s.command != nil && s.command.Process != nil {
		_ = s.command.Process.Kill()
		// `cmd.Wait` — not `Process.Wait`: only the first one fills in the
		// process state, and that is where the kernel's account of the peak
		// is.
		_ = s.command.Wait()
		s.peak = peakRusage(s.command.ProcessState)
	}
	if s.log != nil {
		s.log.Close()
	}
	if s.Dir != "" && !s.keep {
		_ = os.RemoveAll(s.Dir)
	}
}

// PeakBytes: the peak resident set of the server, in bytes.
//
// Zero when the server was not stopped yet — the number only exists once the
// process is gone.
func (s *Server) PeakBytes() uint64 {
	return s.peak
}

// FreePort: a port nobody is listening on.
func FreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
