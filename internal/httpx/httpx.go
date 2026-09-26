// Package httpx is the one outbound HTTP path: HTTPS only, allow-listed hosts, timeouts, size caps.
package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

const MaxBody = 8 << 20

// Client fetches JSON from an allow-listed HTTPS host. Redirects are followed only within the allow-list
// (canirun.ai's apex redirects to www; POST must survive the 307, which net/http handles when GetBody is set).
type Client struct {
	HTTP    *http.Client
	Allowed map[string]bool
	UA      string
}

func New(allowedHosts ...string) *Client {
	c := &Client{Allowed: map[string]bool{}, UA: "aituner/0.1 (+https://github.com/tonynv/aituner)"}
	for _, h := range allowedHosts {
		c.Allowed[h] = true
	}
	c.HTTP = &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" || !c.Allowed[req.URL.Hostname()] {
				return fmt.Errorf("redirect to disallowed host %q", req.URL.Host)
			}
			return nil
		},
	}
	return c
}

func (c *Client) check(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" || !c.Allowed[u.Hostname()] {
		return nil, fmt.Errorf("host %q not allowed", u.Host)
	}
	return u, nil
}

// retryable statuses: rate limiting and transient upstream failures.
func retryable(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusBadGateway || code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout
}

// Backoff is the wait before attempt n (1-based retry number); a var so tests run fast.
var Backoff = func(n int) time.Duration { return time.Duration(n*n) * 700 * time.Millisecond }

const maxAttempts = 3

// JSON performs the request and decodes a JSON body into out. body may be nil. Transient failures
// (network errors, 429, 502/503/504) are retried with backoff, honouring Retry-After (capped at 10s).
func (c *Client) JSON(ctx context.Context, method, rawURL string, body []byte, out any) error {
	u, err := c.check(rawURL)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var wait time.Duration
		var retry bool
		lastErr, wait, retry = c.once(ctx, method, u, body, out)
		if lastErr == nil || !retry || attempt == maxAttempts {
			return lastErr
		}
		if wait == 0 {
			wait = Backoff(attempt)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return lastErr
}

func (c *Client) once(ctx context.Context, method string, u *url.URL, body []byte, out any) (err error, wait time.Duration, retry bool) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return err, 0, false
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err(), 0, false
		}
		return fmt.Errorf("%s: network error: %w", u.Host, err), 0, true
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			wait = time.Duration(min(s, 10)) * time.Second
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("%s is rate limiting requests (HTTP 429); wait a few minutes and retry", u.Host), wait, true
		}
		return &StatusError{Code: resp.StatusCode, Msg: fmt.Sprintf("%s %s: %s", method, u.Host+u.Path, resp.Status)}, wait, retryable(resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxBody)).Decode(out); err != nil {
		return fmt.Errorf("%s: invalid JSON response: %w", u.Host, err), 0, false
	}
	return nil, 0, false
}

// StatusError is a non-200 response, so callers can tell "not found" from a failure.
type StatusError struct {
	Code int
	Msg  string
}

func (e *StatusError) Error() string { return e.Msg }

// Download streams an allow-listed HTTPS URL into a new file dst (created 0600, never overwriting), refusing bodies
// over max bytes, and returns the SHA-256 (hex) and size of what it wrote. A partial file is removed on any error.
func (c *Client) Download(ctx context.Context, rawURL, dst string, max int64) (string, int64, error) {
	u, err := c.check(rawURL)
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Accept", "application/octet-stream")
	dl := *c.HTTP
	dl.Timeout = 30 * time.Minute // a whole download; the connection itself still has the transport's timeouts
	resp, err := dl.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("%s: %w", u.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, &StatusError{Code: resp.StatusCode, Msg: fmt.Sprintf("GET %s: %s", u.Host+u.Path, resp.Status)}
	}
	if resp.ContentLength > max {
		return "", 0, fmt.Errorf("%s is %d bytes, more than the %d allowed", u.Path, resp.ContentLength, max)
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = fmt.Errorf("%s is larger than the %d bytes allowed", u.Path, max)
	}
	if err != nil {
		_ = os.Remove(dst)
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
