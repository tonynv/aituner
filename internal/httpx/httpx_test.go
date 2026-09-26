package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Real TLS server on loopback; the client is pointed at it via its own cert pool and host allow-list.
func setup(t *testing.T, h http.HandlerFunc) (*Client, string) {
	t.Helper()
	Backoff = func(int) time.Duration { return time.Millisecond }
	ts := httptest.NewTLSServer(h)
	t.Cleanup(ts.Close)
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "https://"))
	c := New(host)
	c.HTTP.Transport = ts.Client().Transport
	return c, ts.URL
}

func TestRetriesTransientThenSucceeds(t *testing.T) {
	var n atomic.Int32
	c, u := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	})
	var out struct{ OK bool }
	if err := c.JSON(context.Background(), "GET", u, nil, &out); err != nil || !out.OK || n.Load() != 3 {
		t.Fatalf("err=%v out=%v attempts=%d", err, out, n.Load())
	}
}

func TestPostBodyIsResentOnRetry(t *testing.T) {
	var n atomic.Int32
	var bodies []string
	c, u := setup(t, func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 64)
		k, _ := r.Body.Read(b)
		bodies = append(bodies, string(b[:k]))
		if n.Add(1) == 1 {
			w.WriteHeader(502)
			return
		}
		w.Write([]byte(`{}`))
	})
	var out map[string]any
	if err := c.JSON(context.Background(), "POST", u, []byte(`{"a":1}`), &out); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] != `{"a":1}` || bodies[1] != `{"a":1}` {
		t.Fatalf("bodies %q", bodies)
	}
}

func TestRateLimitGivesClearError(t *testing.T) {
	c, u := setup(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) })
	err := c.JSON(context.Background(), "GET", u, nil, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "rate limiting") {
		t.Fatalf("%v", err)
	}
}

func TestPermanentErrorsAreNotRetried(t *testing.T) {
	var n atomic.Int32
	c, u := setup(t, func(w http.ResponseWriter, r *http.Request) { n.Add(1); w.WriteHeader(404) })
	if err := c.JSON(context.Background(), "GET", u, nil, &struct{}{}); err == nil || n.Load() != 1 {
		t.Fatalf("err=%v attempts=%d", err, n.Load())
	}
}

func TestBadJSONIsAnErrorNotAPanic(t *testing.T) {
	c, u := setup(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<html>`)) })
	if err := c.JSON(context.Background(), "GET", u, nil, &struct{}{}); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("%v", err)
	}
}

func TestHostAllowListAndHTTPSOnly(t *testing.T) {
	c := New("example.com")
	for _, u := range []string{"https://evil.com/x", "http://example.com/x", "file:///etc/passwd"} {
		if err := c.JSON(context.Background(), "GET", u, nil, &struct{}{}); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestRedirectToDisallowedHostRefused(t *testing.T) {
	c, u := setup(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "https://evil.example/x", 307) })
	if err := c.JSON(context.Background(), "GET", u, nil, &struct{}{}); err == nil {
		t.Fatal("redirect to a foreign host must fail")
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	Backoff = func(int) time.Duration { return time.Hour }
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer ts.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "https://"))
	c := New(host)
	c.HTTP.Transport = ts.Client().Transport
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	if err := c.JSON(ctx, "GET", ts.URL, nil, &struct{}{}); err == nil || time.Since(t0) > 2*time.Second {
		t.Fatalf("err=%v elapsed=%v", err, time.Since(t0))
	}
}

func TestDownloadHashesCapsAndNeverOverwrites(t *testing.T) {
	body := strings.Repeat("aituner", 1000)
	c, u := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(body))
	})
	dir := t.TempDir()
	sum, n, err := c.Download(context.Background(), u+"/f", filepath.Join(dir, "a"), 1<<20)
	want := sha256.Sum256([]byte(body))
	if err != nil || n != int64(len(body)) || sum != hex.EncodeToString(want[:]) {
		t.Fatalf("%v %d %s", err, n, sum)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "a")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if _, _, err := c.Download(context.Background(), u+"/f", filepath.Join(dir, "a"), 1<<20); err == nil {
		t.Fatal("overwrote an existing file")
	}
	if _, _, err := c.Download(context.Background(), u+"/f", filepath.Join(dir, "b"), 100); err == nil {
		t.Fatal("accepted a body over the cap")
	}
	if _, err := os.Stat(filepath.Join(dir, "b")); !os.IsNotExist(err) {
		t.Fatal("left a partial file behind")
	}
	var se *StatusError
	if _, _, err := c.Download(context.Background(), u+"/missing", filepath.Join(dir, "c"), 100); !errors.As(err, &se) || se.Code != 404 {
		t.Fatalf("want a 404 StatusError: %v", err)
	}
	if _, _, err := c.Download(context.Background(), "https://example.com/x", filepath.Join(dir, "d"), 100); err == nil {
		t.Fatal("downloaded from a host outside the allow-list")
	}
}
