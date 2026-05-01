// Package client wraps the daemon's HTTP+SSE API for use by the CLI and other
// in-process consumers.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client talks to a running whatsmeow-api daemon.
type Client struct {
	baseURL string
	token   string
	hc      *http.Client
}

// New constructs a Client. baseURL is e.g. "http://127.0.0.1:8080". token is
// the bearer token; pass "" if the daemon has auth disabled.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		hc:      &http.Client{Timeout: 0}, // no timeout: SSE streams are long-lived
	}
}

// Status mirrors the /v1/status response.
type Status struct {
	WAConnected bool   `json:"wa_connected"`
	JID         string `json:"jid"`
	PushName    string `json:"push_name"`
	Since       string `json:"since"`
}

// QREvent matches the daemon's SSE qr/connection events.
type QREvent struct {
	Code     string
	Terminal bool
	Outcome  string
}

// PairEvent matches the daemon's SSE pair_code/connection events.
type PairEvent struct {
	Code     string
	Terminal bool
	Outcome  string
}

// Sentinel errors so callers can branch.
var (
	ErrNotLoggedIn     = errors.New("client: daemon reports not logged in")
	ErrAlreadyLoggedIn = errors.New("client: daemon reports already logged in")
	ErrLoginInProgress = errors.New("client: daemon reports login in progress")
)

func (c *Client) addAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// Status fetches the current connection state.
func (c *Client) Status(ctx context.Context) (Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/status", nil)
	if err != nil {
		return Status{}, err
	}
	c.addAuth(req)
	res, err := c.hc.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Status{}, problemError(res)
	}
	var st Status
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return Status{}, fmt.Errorf("decode status: %w", err)
	}
	return st, nil
}

// Logout requests the daemon to log out.
func (c *Client) Logout(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/logout", nil)
	if err != nil {
		return err
	}
	c.addAuth(req)
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNoContent {
		return nil
	}
	return problemError(res)
}

// LoginQR opens an SSE stream and returns a channel emitting QR events.
// The channel is closed after the terminal event.
func (c *Client) LoginQR(ctx context.Context) (<-chan QREvent, error) {
	res, err := c.openSSE(ctx, "/v1/login/qr", nil)
	if err != nil {
		return nil, err
	}
	out := make(chan QREvent)
	go func() {
		defer close(out)
		defer res.Body.Close()
		for f := range readSSE(ctx, res.Body) {
			switch f.event {
			case "qr":
				var p struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal([]byte(f.data), &p)
				select {
				case out <- QREvent{Code: p.Code}:
				case <-ctx.Done():
					return
				}
			case "connection":
				var p struct {
					Outcome string `json:"outcome"`
				}
				_ = json.Unmarshal([]byte(f.data), &p)
				select {
				case out <- QREvent{Terminal: true, Outcome: p.Outcome}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
	return out, nil
}

// LoginPhone opens an SSE stream for phone-pair login.
func (c *Client) LoginPhone(ctx context.Context, phoneNumber string) (<-chan PairEvent, error) {
	body := bytes.NewBufferString(fmt.Sprintf(`{"phone_number":%q}`, phoneNumber))
	res, err := c.openSSE(ctx, "/v1/login/phone", body)
	if err != nil {
		return nil, err
	}
	out := make(chan PairEvent)
	go func() {
		defer close(out)
		defer res.Body.Close()
		for f := range readSSE(ctx, res.Body) {
			switch f.event {
			case "pair_code":
				var p struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal([]byte(f.data), &p)
				select {
				case out <- PairEvent{Code: p.Code}:
				case <-ctx.Done():
					return
				}
			case "connection":
				var p struct {
					Outcome string `json:"outcome"`
				}
				_ = json.Unmarshal([]byte(f.data), &p)
				select {
				case out <- PairEvent{Terminal: true, Outcome: p.Outcome}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
	return out, nil
}

func (c *Client) openSSE(ctx context.Context, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "text/event-stream")
	c.addAuth(req)
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		err := problemError(res)
		res.Body.Close()
		return nil, err
	}
	return res, nil
}

type sseFrame struct{ event, data string }

func readSSE(ctx context.Context, r io.Reader) <-chan sseFrame {
	out := make(chan sseFrame)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if cur.event != "" || cur.data != "" {
					select {
					case out <- cur:
					case <-ctx.Done():
						return
					}
					cur = sseFrame{}
				}
			}
		}
	}()
	return out
}

type problem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func problemError(res *http.Response) error {
	defer io.Copy(io.Discard, res.Body) //nolint:errcheck
	var p problem
	_ = json.NewDecoder(res.Body).Decode(&p)
	switch p.Code {
	case "wa.not_logged_in":
		return ErrNotLoggedIn
	case "wa.already_logged_in":
		return ErrAlreadyLoggedIn
	case "wa.login_in_progress":
		return ErrLoginInProgress
	}
	return fmt.Errorf("daemon returned %d (%s): %s", res.StatusCode, p.Code, p.Detail)
}
