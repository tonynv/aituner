// Package httpx is the one outbound HTTP path: HTTPS only, allow-listed hosts, timeouts, size caps.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// JSON performs the request and decodes a JSON body into out. body may be nil.
func (c *Client) JSON(ctx context.Context, method, rawURL string, body io.Reader, out any) error {
	u, err := c.check(rawURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("%s %s: %s", method, u.Host+u.Path, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, MaxBody)).Decode(out)
}
