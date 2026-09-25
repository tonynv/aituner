// Package gateway is the only listener editors talk to. It requires an API key, forwards OpenAI-format requests to
// the model server through an explicit field allow-list (pinning every request to the loaded model), and translates
// Anthropic Messages requests (what Claude Code speaks) to and from the OpenAI format.
package gateway

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxBody = 64 << 20

// pinnedModel is mlx_lm.server's alias for "the model given on its command line". Every forwarded request names
// it, because a request naming any other model would make the server load that one instead.
const pinnedModel = "default_model"

// allowed are the only request fields that reach the model server. Anything else (draft_model, adapters, a model
// name, unknown knobs) is dropped.
var allowedChat = map[string]bool{
	"messages": true, "max_tokens": true, "temperature": true, "top_p": true, "top_k": true, "min_p": true, "stop": true,
	"stream": true, "stream_options": true, "tools": true, "tool_choice": true, "seed": true, "presence_penalty": true,
	"frequency_penalty": true, "repetition_penalty": true, "repetition_context_size": true, "logit_bias": true,
	"logprobs": true, "top_logprobs": true, "chat_template_kwargs": true,
}
var allowedCompletions = func() map[string]bool {
	m := map[string]bool{"prompt": true}
	for k := range allowedChat {
		if k != "messages" && k != "tools" && k != "tool_choice" && k != "chat_template_kwargs" {
			m[k] = true
		}
	}
	return m
}()

type Gateway struct {
	Key           string
	ModelID       string // the model name shown to clients
	ContextWindow int    // tokens the loaded model can hold (0 = unknown); shown in /v1/models so clients can be configured truthfully
	// Info, when set, is consulted on every request so the model name and context window are always current (the model can
	// be started, stopped or changed after the gateway was created). It takes precedence over ModelID and ContextWindow.
	Info    func() (model string, contextWindow int)
	Backend func() (string, bool) // internal model-server URL when it is ready
	Client  *http.Client          // no overall timeout: generations can be long
	Log     func(string)
}

func New(key string, backend func() (string, bool), log func(string)) *Gateway {
	if log == nil {
		log = func(string) {}
	}
	return &Gateway{Key: key, Backend: backend, Client: &http.Client{}, Log: log}
}

// NewKey returns a fresh random API key.
func NewKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return "aituner-" + hex.EncodeToString(b)
}

// LoadOrCreateKey reads the key file (0600), creating it on first use.
func LoadOrCreateKey(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		if k := strings.TrimSpace(string(b)); len(k) >= 24 {
			return k, nil
		}
	}
	k := NewKey()
	if err := os.WriteFile(path, []byte(k+"\n"), 0o600); err != nil {
		return "", err
	}
	return k, nil
}

func (g *Gateway) authed(r *http.Request) bool {
	tok := ""
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		tok = v
	} else if v := r.Header.Get("x-api-key"); v != "" {
		tok = v
	}
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(g.Key)) == 1
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func oaError(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"message": msg, "type": kind, "code": kind}})
}

func anError(w http.ResponseWriter, code int, kind, msg string) {
	w.Header().Set("request-id", newID("req_"))
	writeJSON(w, code, AnthropicError(kind, msg))
}

// Handler returns the HTTP handler. Wrap it in a server bound to 127.0.0.1.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		_, up := g.Backend()
		writeJSON(w, 200, map[string]any{"status": "ok", "model_running": up})
	})
	mux.HandleFunc("HEAD /api/hello", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }) // Claude Code's warm-up probe
	mux.HandleFunc("GET /v1/models", g.models)
	mux.HandleFunc("POST /v1/chat/completions", g.openai("/v1/chat/completions", allowedChat))
	mux.HandleFunc("POST /v1/completions", g.openai("/v1/completions", allowedCompletions))
	mux.HandleFunc("POST /v1/messages", g.messages)
	mux.HandleFunc("POST /v1/messages/count_tokens", g.countTokens)
	return mux
}

func (g *Gateway) models(w http.ResponseWriter, r *http.Request) {
	if !g.authed(r) {
		oaError(w, 401, "invalid_api_key", "Invalid API key")
		return
	}
	id, ctxWin := g.ModelID, g.ContextWindow
	if g.Info != nil {
		id, ctxWin = g.Info()
	}
	if id == "" {
		id = "aituner-local"
	}
	now := time.Now()
	writeJSON(w, 200, map[string]any{"object": "list", "has_more": false, "first_id": id, "last_id": id, "data": []map[string]any{
		{"id": id, "object": "model", "type": "model", "display_name": id, "context_window": ctxWin, "max_context_tokens": ctxWin, "created": now.Unix(), "created_at": now.UTC().Format(time.RFC3339), "owned_by": "aituner"}}})
}

// sanitize keeps only allow-listed fields and pins the model.
func sanitize(raw []byte, allow map[string]bool) ([]byte, bool, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, false, errors.New("request body must be a JSON object")
	}
	out := map[string]json.RawMessage{"model": json.RawMessage(`"` + pinnedModel + `"`)}
	for k, v := range in {
		if allow[k] {
			out[k] = v
		}
	}
	if v, ok := in["max_completion_tokens"]; ok { // newer OpenAI name for the same limit
		if _, has := out["max_tokens"]; !has {
			out["max_tokens"] = v
		}
	}
	if v, ok := out["chat_template_kwargs"]; ok {
		if clean := cleanKwargs(v); clean != nil {
			out["chat_template_kwargs"] = clean
		} else {
			delete(out, "chat_template_kwargs")
		}
	}
	var stream bool
	if v, ok := out["stream"]; ok {
		_ = json.Unmarshal(v, &stream)
	}
	b, err := json.Marshal(out)
	return b, stream, err
}

func (g *Gateway) backendURL() (string, bool) { return g.Backend() }

func (g *Gateway) openai(path string, allow map[string]bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !g.authed(r) {
			oaError(w, 401, "invalid_api_key", "Invalid API key")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			oaError(w, 400, "invalid_request_error", "could not read the request body (too large or interrupted)")
			return
		}
		body, stream, err := sanitize(raw, allow)
		if err != nil {
			oaError(w, 400, "invalid_request_error", err.Error())
			return
		}
		base, ok := g.backendURL()
		if !ok {
			oaError(w, 503, "server_error", "no model is running: start one in aituner")
			return
		}
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, base+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := g.Client.Do(req)
		if err != nil {
			oaError(w, 502, "server_error", "the model server is not reachable")
			return
		}
		defer resp.Body.Close()
		for _, h := range []string{"Content-Type", "Cache-Control"} {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		if stream {
			w.Header().Set("X-Accel-Buffering", "no")
		}
		w.WriteHeader(resp.StatusCode)
		copyFlushing(w, resp.Body)
	}
}

// copyFlushing streams the body through, flushing after every read so tokens reach the client as they are produced.
func copyFlushing(w http.ResponseWriter, src io.Reader) {
	f, _ := w.(http.Flusher)
	buf := make([]byte, 8<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if f != nil {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func (g *Gateway) messages(w http.ResponseWriter, r *http.Request) {
	if !g.authed(r) {
		anError(w, 401, "authentication_error", "invalid x-api-key / bearer token")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		anError(w, 400, "invalid_request_error", "could not read the request body (too large or interrupted)")
		return
	}
	tr, err := ToOpenAI(raw)
	if err != nil {
		anError(w, 400, "invalid_request_error", err.Error())
		return
	}
	model, stream := tr.Model, tr.Stream
	base, ok := g.backendURL()
	if !ok {
		anError(w, 503, "api_error", "no model is running: start one in aituner")
		return
	}
	// route through the same allow-list as the OpenAI endpoint: one choke point for what reaches the server
	enc, _ := json.Marshal(tr.Body)
	body, _, err := sanitize(enc, allowedChat)
	if err != nil {
		anError(w, 400, "invalid_request_error", err.Error())
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.Client.Do(req)
	if err != nil {
		anError(w, 502, "api_error", "the model server is not reachable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		kind := "api_error"
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			kind = "invalid_request_error"
		}
		anError(w, resp.StatusCode, kind, upstreamMessage(b))
		return
	}
	w.Header().Set("request-id", newID("req_"))
	if !stream {
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		if err != nil {
			anError(w, 502, "api_error", "the model server response was interrupted")
			return
		}
		msg, err := FromOpenAI(b, model, tr.Tools)
		if err != nil {
			anError(w, 502, "api_error", err.Error())
			return
		}
		writeJSON(w, 200, msg)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	f, _ := w.(http.Flusher)
	if err := StreamToAnthropic(w, f, resp.Body, model, tr.Tools); err != nil {
		g.Log("gateway: stream: " + err.Error())
	}
}

func upstreamMessage(b []byte) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil {
		switch v := e.Error.(type) {
		case string:
			return v
		case map[string]any:
			if m, ok := v["message"].(string); ok {
				return m
			}
		}
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		if len(s) > 300 {
			s = s[:300]
		}
		return s
	}
	return "the model server rejected the request"
}

// countTokens gives a rough estimate (about 3.5 characters per token) so clients that ask do not have to fall back.
// It is an estimate, and says so in the response header.
func (g *Gateway) countTokens(w http.ResponseWriter, r *http.Request) {
	if !g.authed(r) {
		anError(w, 401, "authentication_error", "invalid x-api-key / bearer token")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		anError(w, 400, "invalid_request_error", "could not read the request body")
		return
	}
	tr, err := ToOpenAI(raw)
	if err != nil {
		anError(w, 400, "invalid_request_error", err.Error())
		return
	}
	b, _ := json.Marshal(tr.Body)
	w.Header().Set("x-aituner-estimate", "characters/3.5")
	writeJSON(w, 200, map[string]any{"input_tokens": int(float64(len(b)) / 3.5)})
}

// cleanKwargs keeps chat_template_kwargs to what template switches look like in practice (for example
// {"enable_thinking": false}): at most 16 keys, values that are booleans, numbers or short strings. Anything else is
// dropped, so caller-controlled structure never reaches the template engine.
func cleanKwargs(raw json.RawMessage) json.RawMessage {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil || len(in) == 0 || len(in) > 16 {
		return nil
	}
	out := map[string]any{}
	for k, v := range in {
		if len(k) > 64 {
			continue
		}
		switch x := v.(type) {
		case bool, float64:
			out[k] = x
		case string:
			if len(x) <= 256 {
				out[k] = x
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	b, _ := json.Marshal(out)
	return b
}
