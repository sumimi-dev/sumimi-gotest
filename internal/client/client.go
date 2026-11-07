// Package client speaks the Sumimi API.
//
// **The standard library only.** The tool has to run on a machine that has
// nothing on it but the server and this binary: no toolchain, no modules to
// download, no runtime to install. That is the whole point — the machine
// being measured is usually a plain VPS.
//
// The client is also a **second implementation**: it speaks HTTP, not the
// server's types. A probe that shares code with the server cannot disagree
// with it, and a probe that cannot disagree cannot find anything.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// A client for one Sumimi server.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// New: a client for the server at the base URL.
func New(base string) *Client {
	return &Client{
		base: strings.TrimSuffix(base, "/"),
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 256,
				IdleConnTimeout:     90 * time.Second,
				// The payloads are small and the server does not need to
				// spend time compressing an answer we throw away.
				DisableCompression: true,
			},
		},
	}
}

// The URL of an endpoint on the server.
func (c *Client) URL(path string) string {
	return c.base + path
}

// The token, once there is one.
func (c *Client) Token() string { return c.token }

// Login: `POST /api/login` — the answer carries the token.
func (c *Client) Login(ctx context.Context, user, password string) error {
	body, err := json.Marshal(map[string]string{
		"username": user,
		"password": password,
	})
	if err != nil {
		return err
	}
	answer, status, err := c.do(ctx, http.MethodPost, "/api/login", body, "")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("the login was refused: %d %s", status, answer)
	}
	var parsed struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(answer, &parsed); err != nil {
		return fmt.Errorf("the login answer is not JSON: %w", err)
	}
	if parsed.Token == "" {
		return fmt.Errorf("the login answer has no token: %s", answer)
	}
	c.token = parsed.Token
	return nil
}

// CreateWebsite: `POST /api/websites` — the answer carries the id.
//
// The load goes to a website of its own, so it does not pollute somebody
// else's numbers.
func (c *Client) CreateWebsite(ctx context.Context, name string) (string, error) {
	if c.token == "" {
		return "", fmt.Errorf("not logged in")
	}
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return "", err
	}
	answer, status, err := c.do(ctx, http.MethodPost, "/api/websites", body, c.token)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return "", fmt.Errorf("the website was not created: %d %s", status, answer)
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(answer, &parsed); err != nil {
		return "", fmt.Errorf("the answer is not JSON: %w", err)
	}
	if parsed.ID == "" {
		return "", fmt.Errorf("the answer has no id: %s", answer)
	}
	return parsed.ID, nil
}

// Heartbeat: `GET /api/heartbeat` — what the server says about itself.
//
// This is the endpoint the reports are built on: it carries the server's
// counters, the server's own memory and the machine the server runs on.
func (c *Client) Heartbeat(ctx context.Context) (*Heartbeat, error) {
	answer, status, err := c.do(ctx, http.MethodGet, "/api/heartbeat", nil, "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("the heartbeat was refused: %d %s", status, answer)
	}
	var parsed Heartbeat
	if err := json.Unmarshal(answer, &parsed); err != nil {
		return nil, fmt.Errorf("the heartbeat is not JSON: %w", err)
	}
	return &parsed, nil
}

// One request. The status code is the answer; the body is only read when
// something went wrong.
func (c *Client) do(
	ctx context.Context,
	method string,
	path string,
	body []byte,
	token string,
) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.URL(path), bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.StatusCode, err
	}
	return answer, response.StatusCode, nil
}

// What the server says about itself.
//
// The fields are the ones the reports need: the counters, the memory and the
// machine. The ones that are not there are optional — the heartbeat grew over
// time, and an older server simply does not know some of them.
type Heartbeat struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version"`
	Uptime    uint64 `json:"uptimeSeconds"`
	Queued    uint64 `json:"queued"`
	Dropped   uint64 `json:"dropped"`
	Websites  int64  `json:"websites"`
	Memory    int64  `json:"duckdbMemoryBytes"`
	Limit     string `json:"duckdbMemoryLimit"`
	LimitReal string `json:"duckdbMemoryLimitEffective"`
	Threads   int64  `json:"duckdbThreads"`
	Flushes   uint64 `json:"flushes"`
	FlushP50  uint64 `json:"flushP50Ms"`
	FlushP99  uint64 `json:"flushP99Ms"`
	FlushMax  uint64 `json:"flushMaxMs"`
	Database  int64  `json:"databaseSizeBytes"`

	// The gate.
	InflightRequests uint64 `json:"inflightRequests"`
	InflightBytes    uint64 `json:"inflightBytes"`
	RefusedRate      uint64 `json:"refusedRate"`
	RefusedInflight  uint64 `json:"refusedInflight"`
	RefusedBody      uint64 `json:"refusedBody"`
	AdmitRPS         uint64 `json:"admitRps"`

	// The process and the machine.
	RSSBytes     uint64   `json:"rssBytes"`
	PeakRSSBytes uint64   `json:"peakRssBytes"`
	Machine      *Machine `json:"machine"`
}

// The machine the server runs on.
type Machine struct {
	CPU      string `json:"cpu,omitempty"`
	Cores    uint32 `json:"cores,omitempty"`
	RAMBytes uint64 `json:"ramBytes,omitempty"`
	OS       string `json:"os,omitempty"`
	Arch     string `json:"arch,omitempty"`
}

// Payload: the body a collection request carries.
//
// The size matters as much as the rate: the write path's capacity is in
// **bytes a second**, not in rows a second — so a bigger body changes what a
// server can take.
//
// The body is built by hand rather than marshalled from a map, because the
// size has to be **exact**: the filler is exactly as long as it needs to be.
// A body that is 7 bytes bigger than it should be is a different
// measurement.
func Payload(website string, size int) []byte {
	const head = `{"website":"`
	const middle = `","payload":{"url":"/load","title":"Load","referrer":"https://news.ycombinator.com/"}`
	const filler = `,"filler":"`

	// The smallest body that still says everything.
	small := head + website + middle + "}"
	if size <= len(small) {
		return []byte(small)
	}

	// The filler field's own name costs bytes, so there is a size below which
	// the body cannot be made exactly.
	exact := head + website + middle + filler + `"}`
	if size < len(exact) {
		return []byte(small)
	}
	return []byte(head + website + middle + filler + strings.Repeat("x", size-len(exact)) + `"}`)
}
