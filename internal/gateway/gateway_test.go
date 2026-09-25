package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Fixtures in testdata/ are real responses captured from mlx_lm.server 0.31.3 (Llama-3.1-8B-Instruct-4bit and
// Qwen2.5-Coder-1.5B-Instruct-8bit) on the reference machine.
func fx(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var weather = ToolSet{"get_weather": nil}

func TestToOpenAIMessagesSystemToolsAndToolResults(t *testing.T) {
	req := `{"model":"claude-sonnet-x","max_tokens":512,"temperature":0.2,"top_p":0.9,"top_k":40,"stop_sequences":["END"],"stream":true,
	 "system":[{"type":"text","text":"You are terse."},{"type":"text","text":"Answer in English."}],
	 "tools":[{"name":"get_weather","description":"Weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}},{"name":"web_search","type":"web_search_20250305"}],
	 "tool_choice":{"type":"tool","name":"get_weather"},
	 "messages":[
	  {"role":"user","content":"Weather in Paris?"},
	  {"role":"assistant","content":[{"type":"text","text":"Checking."},{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Paris"}}]},
	  {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"18C, sunny"}]},{"type":"text","text":"Thanks, summarise."},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}
	 ]}`
	tr, err := ToOpenAI([]byte(req))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(tr.Body)
	var got struct {
		Messages []map[string]any `json:"messages"`
		Tools    []map[string]any `json:"tools"`
		Choice   map[string]any   `json:"tool_choice"`
		Stop     []string         `json:"stop"`
		Stream   bool             `json:"stream"`
		Opts     map[string]any   `json:"stream_options"`
		Max      int              `json:"max_tokens"`
		Temp     float64          `json:"temperature"`
		TopK     int              `json:"top_k"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if tr.Model != "claude-sonnet-x" || !tr.Stream || len(tr.Tools) != 1 || !tr.Tools.Has("get_weather") {
		t.Fatalf("meta: %+v", tr)
	}
	m := got.Messages
	if len(m) != 5 {
		t.Fatalf("want system, user, assistant, tool, user; got %d: %s", len(m), b)
	}
	if m[0]["role"] != "system" || m[0]["content"] != "You are terse.\n\nAnswer in English." {
		t.Fatalf("system: %v", m[0])
	}
	if m[2]["role"] != "assistant" || m[2]["content"] != "Checking." {
		t.Fatalf("assistant: %v", m[2])
	}
	call := m[2]["tool_calls"].([]any)[0].(map[string]any)
	fn := call["function"].(map[string]any)
	if call["id"] != "toolu_1" || fn["name"] != "get_weather" || fn["arguments"] != `{"city":"Paris"}` {
		t.Fatalf("tool call: %v", call)
	}
	// the tool result must come right after the assistant call, before the user's follow-up text
	if m[3]["role"] != "tool" || m[3]["tool_call_id"] != "toolu_1" || m[3]["content"] != "18C, sunny" {
		t.Fatalf("tool result: %v", m[3])
	}
	if m[4]["role"] != "user" || !strings.Contains(m[4]["content"].(string), "Thanks, summarise.") || !strings.Contains(m[4]["content"].(string), "text-only") {
		t.Fatalf("user follow-up (with the image noted, not silently dropped): %v", m[4])
	}
	if len(got.Tools) != 1 || got.Tools[0]["function"].(map[string]any)["name"] != "get_weather" {
		t.Fatalf("server-side tools have no schema and must be skipped: %v", got.Tools)
	}
	if got.Choice["function"].(map[string]any)["name"] != "get_weather" || got.Max != 512 || got.Temp != 0.2 || got.TopK != 40 || got.Stop[0] != "END" || !got.Stream || got.Opts["include_usage"] != true {
		t.Fatalf("params: %s", b)
	}
}

func TestToOpenAIRejectsBadRequests(t *testing.T) {
	for name, req := range map[string]string{
		"not json": `nope`, "no messages": `{"messages":[]}`, "bad role": `{"messages":[{"role":"tool","content":"x"}]}`,
		"only system": `{"messages":[{"role":"system","content":"x"}]}`,
		"bad content": `{"messages":[{"role":"user","content":42}]}`, "bad system": `{"system":42,"messages":[{"role":"user","content":"x"}]}`,
	} {
		if _, err := ToOpenAI([]byte(req)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	tr, err := ToOpenAI([]byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil || tr.Body["max_tokens"] != 4096 || tr.Body["stream"] != nil {
		t.Fatalf("defaults: %+v %v", tr.Body, err)
	}
	tc := func(choice string) any {
		r, _ := ToOpenAI([]byte(`{"messages":[{"role":"user","content":"x"}],"tools":[{"name":"a","input_schema":{}}],"tool_choice":` + choice + `}`))
		return r.Body["tool_choice"]
	}
	if tc(`{"type":"auto"}`) != "auto" || tc(`{"type":"any"}`) != "required" || tc(`{"type":"none"}`) != "none" {
		t.Fatal("tool_choice mapping")
	}
}

func TestFromOpenAIRealTextResponse(t *testing.T) {
	msg, err := FromOpenAI(fx(t, "oa_text.json"), "claude-x", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := msg["content"].([]map[string]any)
	if msg["type"] != "message" || msg["role"] != "assistant" || msg["model"] != "claude-x" || len(c) != 1 || c[0]["text"] != "Hello, how are you today?" || msg["stop_reason"] != "end_turn" {
		t.Fatalf("%v", msg)
	}
	u := msg["usage"].(map[string]any)
	if u["input_tokens"] != 41 || u["output_tokens"] != 8 || !strings.HasPrefix(msg["id"].(string), "msg_") {
		t.Fatalf("usage/id: %v", msg)
	}
}

// Real captures: neither model returned structured tool_calls, so the gateway must recover them from the text.
func TestFromOpenAIRecoversRealToolCallsFromText(t *testing.T) {
	for _, f := range []string{"oa_tool.json", "qwen_tool.json"} {
		msg, err := FromOpenAI(fx(t, f), "m", weather)
		if err != nil {
			t.Fatal(f, err)
		}
		c := msg["content"].([]map[string]any)
		if len(c) != 1 || c[0]["type"] != "tool_use" || c[0]["name"] != "get_weather" || msg["stop_reason"] != "tool_use" {
			t.Fatalf("%s: %v", f, msg)
		}
		if !strings.HasPrefix(c[0]["id"].(string), "toolu_") {
			t.Fatalf("%s: id %v", f, c[0]["id"])
		}
		in, _ := json.Marshal(c[0]["input"])
		if string(in) != `{"city":"Paris"}` && !strings.Contains(strings.ReplaceAll(string(in), " ", ""), `"city":"Paris"`) {
			t.Fatalf("%s: input %s", f, in)
		}
	}
	// with NO declared tools, the same text is just text (and control tokens are still stripped)
	msg, _ := FromOpenAI(fx(t, "qwen_tool.json"), "m", nil)
	txt := msg["content"].([]map[string]any)[0]["text"].(string)
	if strings.Contains(txt, "<|im_end|>") || !strings.Contains(txt, "get_weather") || msg["stop_reason"] != "end_turn" {
		t.Fatalf("undeclared tools must not become tool calls: %v", msg)
	}
}

func TestExtractToolCalls(t *testing.T) {
	names := ToolSet{"read_file": nil, "bash": nil}
	cases := []struct {
		name, in string
		calls    []string
		prose    string
	}{
		{"hermes", "Let me look.\n<tool_call>\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"a.go\"}}\n</tool_call>", []string{"read_file"}, "Let me look."},
		{"hermes two", "<tool_call>{\"name\":\"read_file\",\"arguments\":{}}</tool_call><tool_call>{\"name\":\"bash\",\"arguments\":{\"cmd\":\"ls\"}}</tool_call>", []string{"read_file", "bash"}, ""},
		{"no closing tag", "<tool_call>{\"name\":\"bash\",\"arguments\":{\"cmd\":\"ls\"}}", []string{"bash"}, ""},
		{"python tag", "<|python_tag|>{\"name\": \"bash\", \"parameters\": {\"cmd\": \"ls\"}}", []string{"bash"}, ""},
		{"fenced json", "```json\n{\"name\":\"bash\",\"arguments\":{\"cmd\":\"ls\"}}\n```", []string{"bash"}, ""},
		{"bare array", `[{"name":"bash","arguments":{"cmd":"a"}},{"name":"read_file","arguments":{"path":"b"}}]`, []string{"bash", "read_file"}, ""},
		{"openai shaped", `{"function":{"name":"bash","arguments":"{\"cmd\":\"ls\"}"}}`, []string{"bash"}, ""},
		{"string arguments", `{"name":"bash","arguments":"{\"cmd\":\"ls\"}"}`, []string{"bash"}, ""},
		{"undeclared name", `{"name":"rm_rf","arguments":{}}`, nil, `{"name":"rm_rf","arguments":{}}`},
		{"json in prose", "The config looks like {\"name\":\"bash\",\"arguments\":{}} in the file.", nil, "The config looks like {\"name\":\"bash\",\"arguments\":{}} in the file."},
		{"python code block", "```python\nprint('hi')\n```", nil, "```python\nprint('hi')\n```"},
		{"plain", "Just an answer.", nil, "Just an answer."},
		{"special tokens", "Done.<|im_end|>", nil, "Done."},
	}
	for _, c := range cases {
		prose, calls := ExtractToolCalls(c.in, names)
		var got []string
		for _, x := range calls {
			got = append(got, x.Name)
			var obj map[string]any
			if json.Unmarshal(x.Args, &obj) != nil {
				t.Errorf("%s: arguments are not a JSON object: %s", c.name, x.Args)
			}
		}
		if strings.Join(got, ",") != strings.Join(c.calls, ",") || prose != c.prose {
			t.Errorf("%s: calls=%v prose=%q, want calls=%v prose=%q", c.name, got, prose, c.calls, c.prose)
		}
	}
}

// ---- streaming ----------------------------------------------------------------------------------------------------

type ev struct {
	Name string
	Data map[string]any
}

func parseSSE(t *testing.T, s string) []ev {
	t.Helper()
	var out []ev
	for _, blk := range strings.Split(strings.TrimSpace(s), "\n\n") {
		var e ev
		for _, l := range strings.Split(blk, "\n") {
			if v, ok := strings.CutPrefix(l, "event: "); ok {
				e.Name = v
			}
			if v, ok := strings.CutPrefix(l, "data: "); ok {
				if err := json.Unmarshal([]byte(v), &e.Data); err != nil {
					t.Fatalf("bad event data %q: %v", v, err)
				}
			}
		}
		out = append(out, e)
	}
	return out
}

// checkStream asserts the Anthropic event-sequence invariants any client relies on.
func checkStream(t *testing.T, evs []ev) (text string, tools []map[string]string, stop string, in, outTok int) {
	t.Helper()
	if len(evs) < 3 || evs[0].Name != "message_start" || evs[len(evs)-1].Name != "message_stop" {
		t.Fatalf("stream must start with message_start and end with message_stop: %v", names(evs))
	}
	openIdx, next := -1, 0
	tool := map[int]map[string]string{}
	for _, e := range evs[1 : len(evs)-1] {
		switch e.Name {
		case "content_block_start":
			idx := int(e.Data["index"].(float64))
			if openIdx != -1 || idx != next {
				t.Fatalf("blocks must be sequential and never overlap: start %d while open=%d next=%d", idx, openIdx, next)
			}
			openIdx, next = idx, next+1
			cb := e.Data["content_block"].(map[string]any)
			if cb["type"] == "tool_use" {
				tool[idx] = map[string]string{"id": cb["id"].(string), "name": cb["name"].(string)}
			}
		case "content_block_delta":
			idx, d := int(e.Data["index"].(float64)), e.Data["delta"].(map[string]any)
			if idx != openIdx {
				t.Fatalf("delta for block %d while %d is open", idx, openIdx)
			}
			switch d["type"] {
			case "text_delta":
				text += d["text"].(string)
			case "input_json_delta":
				tool[idx]["json"] += d["partial_json"].(string)
			}
		case "content_block_stop":
			if int(e.Data["index"].(float64)) != openIdx {
				t.Fatalf("stop for the wrong block")
			}
			openIdx = -1
		case "message_delta":
			stop = e.Data["delta"].(map[string]any)["stop_reason"].(string)
			u := e.Data["usage"].(map[string]any)
			in, outTok = int(u["input_tokens"].(float64)), int(u["output_tokens"].(float64))
		}
	}
	if openIdx != -1 {
		t.Fatal("a content block was left open")
	}
	for i := 0; i < next; i++ {
		if tl, ok := tool[i]; ok {
			var probe map[string]any
			if json.Unmarshal([]byte(tl["json"]), &probe) != nil {
				t.Fatalf("tool input is not valid JSON: %q", tl["json"])
			}
			tools = append(tools, tl)
		}
	}
	return
}

func names(evs []ev) []string {
	var n []string
	for _, e := range evs {
		n = append(n, e.Name)
	}
	return n
}

func stream(t *testing.T, fixture string, tools ToolSet) []ev {
	t.Helper()
	var buf bytes.Buffer
	if err := StreamToAnthropic(&buf, nil, bytes.NewReader(fx(t, fixture)), "claude-x", tools); err != nil {
		t.Fatal(err)
	}
	return parseSSE(t, buf.String())
}

func TestStreamRealTextResponse(t *testing.T) {
	text, tools, stop, in, out := checkStream(t, stream(t, "oa_text_stream.sse", nil))
	if text != "Hello, how are you today?" || len(tools) != 0 || stop != "end_turn" || in != 41 || out != 8 {
		t.Fatalf("text=%q tools=%v stop=%s usage=%d/%d", text, tools, stop, in, out)
	}
}

func TestStreamRealToolCallsRecoveredFromText(t *testing.T) {
	for _, f := range []string{"oa_tool_stream.sse", "qwen_tool_stream.sse"} {
		text, tools, stop, _, _ := checkStream(t, stream(t, f, weather))
		if len(tools) != 1 || tools[0]["name"] != "get_weather" || stop != "tool_use" {
			t.Fatalf("%s: text=%q tools=%v stop=%s", f, text, tools, stop)
		}
		if !strings.Contains(strings.ReplaceAll(tools[0]["json"], " ", ""), `"city":"Paris"`) {
			t.Fatalf("%s: input %s", f, tools[0]["json"])
		}
		if strings.Contains(text, "python_tag") || strings.Contains(text, "im_end") || strings.Contains(text, "```") {
			t.Fatalf("%s: tool-call markup leaked into the visible text: %q", f, text)
		}
	}
	// declared no tools: the very same text streams through as ordinary text
	text, tools, stop, _, _ := checkStream(t, stream(t, "qwen_tool_stream.sse", nil))
	if len(tools) != 0 || stop != "end_turn" || !strings.Contains(text, "get_weather") || strings.Contains(text, "im_end") {
		t.Fatalf("text=%q tools=%v stop=%s", text, tools, stop)
	}
}

func chunk(delta string, finish string) string {
	f := "null"
	if finish != "" {
		f = `"` + finish + `"`
	}
	return `data: {"choices":[{"index":0,"delta":{"content":` + delta + `},"finish_reason":` + f + `}]}` + "\n\n"
}

// Answers that begin like a tool call but are not one must stream out intact, and code answers must not be held back.
func TestStreamHoldsOnlyWhatMightBeAToolCall(t *testing.T) {
	run := func(sse string) (string, []ev) {
		var buf bytes.Buffer
		if err := StreamToAnthropic(&buf, nil, strings.NewReader(sse+"data: [DONE]\n\n"), "m", weather); err != nil {
			t.Fatal(err)
		}
		evs := parseSSE(t, buf.String())
		text, _, _, _, _ := checkStream(t, evs)
		return text, evs
	}
	// a python code answer is released at once: the first text delta appears before the stream ends
	text, evs := run(chunk("\"```py\"", "") + chunk("\"thon\\nprint(1)\\n\"", "") + chunk("\"```\"", "stop"))
	if text != "```python\nprint(1)\n```" {
		t.Fatalf("code answer: %q", text)
	}
	if first := names(evs)[2]; first != "content_block_delta" {
		t.Fatalf("events: %v", names(evs))
	}
	// JSON that is not a declared tool call
	if text, _ = run(chunk(`"{\"a\":"`, "") + chunk(`" 1}"`, "stop")); text != `{"a": 1}` {
		t.Fatalf("plain JSON answer: %q", text)
	}
	// an unfinished held marker at the end of the message is flushed as text, never lost
	if text, _ = run(chunk(`"<tool"`, "stop")); text != "<tool" {
		t.Fatalf("dangling marker: %q", text)
	}
	// prose then a tool call in hermes format: prose is shown, the call is extracted
	var buf bytes.Buffer
	sse := chunk(`"I will check. "`, "") + chunk(`"<tool_call>{\"name\":\"get_weather\","`, "") + chunk(`"\"arguments\":{\"city\":\"Rome\"}}</tool_call>"`, "stop") + "data: [DONE]\n\n"
	if err := StreamToAnthropic(&buf, nil, strings.NewReader(sse), "m", weather); err != nil {
		t.Fatal(err)
	}
	tx, tools, stop, _, _ := checkStream(t, parseSSE(t, buf.String()))
	if len(tools) != 1 || tools[0]["name"] != "get_weather" || stop != "tool_use" || !strings.HasPrefix(tx, "I will check.") {
		t.Fatalf("text=%q tools=%v stop=%s", tx, tools, stop)
	}
}

func TestStreamStructuredToolCallsFromServer(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather","arguments":""}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Rome\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}` + "\n\n" + "data: [DONE]\n\n"
	var buf bytes.Buffer
	if err := StreamToAnthropic(&buf, nil, strings.NewReader(sse), "m", weather); err != nil {
		t.Fatal(err)
	}
	_, tools, stop, in, out := checkStream(t, parseSSE(t, buf.String()))
	if len(tools) != 1 || tools[0]["id"] != "call_1" || tools[0]["json"] != `{"city":"Rome"}` || stop != "tool_use" || in != 10 || out != 5 {
		t.Fatalf("%v %s %d %d", tools, stop, in, out)
	}
}

func TestStreamEmptyUpstreamIsAnErrorEvent(t *testing.T) {
	var buf bytes.Buffer
	if err := StreamToAnthropic(&buf, nil, strings.NewReader("data: [DONE]\n\n"), "m", nil); err == nil {
		t.Fatal("empty upstream must be an error")
	}
	if evs := parseSSE(t, buf.String()); len(evs) != 1 || evs[0].Name != "error" {
		t.Fatalf("%v", evs)
	}
}

// ---- HTTP layer ---------------------------------------------------------------------------------------------------

type captured struct {
	mu   sync.Mutex
	path string
	body map[string]any
}

// backendServing replays REAL captured server output and records what the gateway sent it.
func backendServing(t *testing.T, cap *captured, respond func(w http.ResponseWriter, path string)) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cap.mu.Lock()
		cap.path = r.URL.Path
		_ = json.Unmarshal(b, &cap.body)
		cap.mu.Unlock()
		respond(w, r.URL.Path)
	}))
	t.Cleanup(s.Close)
	return s
}

func newGW(t *testing.T, backend *httptest.Server) (*Gateway, *httptest.Server) {
	t.Helper()
	g := New("aituner-testkey-0123456789abcdef", func() (string, bool) {
		if backend == nil {
			return "", false
		}
		return backend.URL, true
	}, nil)
	g.ModelID = "mlx-community/Test-4bit"
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)
	return g, srv
}

func call(t *testing.T, srv *httptest.Server, method, path, body string, hdr map[string]string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

var bearer = map[string]string{"Authorization": "Bearer aituner-testkey-0123456789abcdef"}

func TestGatewayAuthenticatesEveryEndpointInTheRightErrorShape(t *testing.T) {
	_, srv := newGW(t, nil)
	for _, path := range []string{"/v1/chat/completions", "/v1/completions"} {
		code, body, _ := call(t, srv, "POST", path, `{}`, nil)
		if code != 401 || !strings.Contains(body, "invalid_api_key") {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
		code, body, _ := call(t, srv, "POST", path, `{}`, map[string]string{"x-api-key": "wrong"})
		if code != 401 || !strings.Contains(body, `"type":"authentication_error"`) {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
	if code, _, _ := call(t, srv, "GET", "/v1/models", "", nil); code != 401 {
		t.Errorf("models without key: %d", code)
	}
	for _, h := range []map[string]string{bearer, {"x-api-key": "aituner-testkey-0123456789abcdef"}} {
		if code, body, _ := call(t, srv, "GET", "/v1/models", "", h); code != 200 || !strings.Contains(body, "mlx-community/Test-4bit") {
			t.Errorf("models with a valid key: %d %s", code, body)
		}
	}
	if code, body, _ := call(t, srv, "GET", "/health", "", nil); code != 200 || !strings.Contains(body, `"model_running":false`) {
		t.Errorf("health: %d %s", code, body)
	}
	if code, _, _ := call(t, srv, "HEAD", "/api/hello", "", nil); code != 200 {
		t.Errorf("Claude Code's warm-up probe: %d", code)
	}
	for _, k := range []string{"", "Bearer ", "Bearer aituner-testkey-0123456789abcdeX", "Basic abc"} {
		if code, _, _ := call(t, srv, "GET", "/v1/models", "", map[string]string{"Authorization": k}); code != 401 {
			t.Errorf("credential %q accepted", k)
		}
	}
}

func TestGatewayNoModelRunningIsAClearError(t *testing.T) {
	_, srv := newGW(t, nil)
	code, body, _ := call(t, srv, "POST", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`, bearer)
	if code != 503 || !strings.Contains(body, "start one in aituner") {
		t.Fatalf("openai: %d %s", code, body)
	}
	code, body, _ = call(t, srv, "POST", "/v1/messages", `{"messages":[{"role":"user","content":"hi"}]}`, bearer)
	if code != 503 || !strings.Contains(body, `"type":"error"`) || !strings.Contains(body, "start one in aituner") {
		t.Fatalf("anthropic: %d %s", code, body)
	}
}

// The model server loads whatever model a request names and honours draft_model / adapter fields. The gateway must
// never let those through.
func TestGatewayPinsTheModelAndDropsUnsafeFields(t *testing.T) {
	var cap captured
	be := backendServing(t, &cap, func(w http.ResponseWriter, _ string) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fx(t, "oa_text.json"))
	})
	_, srv := newGW(t, be)
	evil := `{"model":"attacker/huge-model","draft_model":"attacker/draft","adapters":"/etc","adapter_path":"/etc/passwd","num_draft_tokens":99,
	 "trust_remote_code":true,"messages":[{"role":"user","content":"hi"}],"max_completion_tokens":77,"temperature":0.5,"unknown_knob":1}`
	code, _, _ := call(t, srv, "POST", "/v1/chat/completions", evil, bearer)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.path != "/v1/chat/completions" || cap.body["model"] != "default_model" {
		t.Fatalf("model must be pinned: %v", cap.body)
	}
	for _, bad := range []string{"draft_model", "adapters", "adapter_path", "num_draft_tokens", "trust_remote_code", "unknown_knob", "max_completion_tokens"} {
		if _, ok := cap.body[bad]; ok {
			t.Errorf("%s reached the model server", bad)
		}
	}
	if cap.body["max_tokens"] != float64(77) || cap.body["temperature"] != 0.5 || cap.body["messages"] == nil {
		t.Fatalf("legitimate fields must survive: %v", cap.body)
	}
}

func TestGatewayAnthropicRouteAlsoGoesThroughTheAllowList(t *testing.T) {
	var cap captured
	be := backendServing(t, &cap, func(w http.ResponseWriter, _ string) { w.Write(fx(t, "oa_text.json")) })
	_, srv := newGW(t, be)
	code, body, hdr := call(t, srv, "POST", "/v1/messages", `{"model":"claude-opus-9","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`, map[string]string{"x-api-key": "aituner-testkey-0123456789abcdef"})
	if code != 200 || hdr.Get("request-id") == "" || !strings.Contains(body, `"type":"message"`) || !strings.Contains(body, "claude-opus-9") {
		t.Fatalf("%d %s", code, body)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.body["model"] != "default_model" || cap.path != "/v1/chat/completions" {
		t.Fatalf("%v %s", cap.body, cap.path)
	}
}

func TestGatewayStreamsAnthropicEventsToTheClient(t *testing.T) {
	be := backendServing(t, &captured{}, func(w http.ResponseWriter, _ string) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write(fx(t, "oa_tool_stream.sse"))
	})
	_, srv := newGW(t, be)
	body := `{"model":"m","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"weather in Paris"}],"tools":[{"name":"get_weather","input_schema":{"type":"object"}}]}`
	code, out, hdr := call(t, srv, "POST", "/v1/messages", body, bearer)
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%d %v", code, hdr)
	}
	_, tools, stop, _, _ := checkStream(t, parseSSE(t, out))
	if len(tools) != 1 || tools[0]["name"] != "get_weather" || stop != "tool_use" {
		t.Fatalf("%v %s\n%s", tools, stop, out)
	}
}

func TestGatewayTranslatesUpstreamErrors(t *testing.T) {
	be := backendServing(t, &captured{}, func(w http.ResponseWriter, _ string) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"prompt is too long for this model"}`))
	})
	_, srv := newGW(t, be)
	code, body, _ := call(t, srv, "POST", "/v1/messages", `{"messages":[{"role":"user","content":"x"}]}`, bearer)
	if code != 400 || !strings.Contains(body, "invalid_request_error") || !strings.Contains(body, "prompt is too long") {
		t.Fatalf("%d %s", code, body)
	}
	// and OpenAI clients get the server's own answer through unchanged
	code, body, _ = call(t, srv, "POST", "/v1/chat/completions", `{"messages":[{"role":"user","content":"x"}]}`, bearer)
	if code != 400 || !strings.Contains(body, "prompt is too long") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestGatewayRejectsMalformedAndOversizedBodies(t *testing.T) {
	_, srv := newGW(t, nil)
	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
		if code, _, _ := call(t, srv, "POST", path, `not json`, bearer); code != 400 {
			t.Errorf("%s malformed: %d", path, code)
		}
		if code, _, _ := call(t, srv, "POST", path, `[1,2,3]`, bearer); code != 400 {
			t.Errorf("%s non-object: %d", path, code)
		}
	}
	big := `{"messages":[{"role":"user","content":"` + strings.Repeat("a", maxBody+10) + `"}]}`
	if code, _, _ := call(t, srv, "POST", "/v1/chat/completions", big, bearer); code != 400 {
		t.Errorf("oversized body: %d", code)
	}
}

func TestCountTokensIsARoughEstimate(t *testing.T) {
	_, srv := newGW(t, nil)
	code, body, hdr := call(t, srv, "POST", "/v1/messages/count_tokens", `{"messages":[{"role":"user","content":"`+strings.Repeat("word ", 200)+`"}]}`, bearer)
	var r struct{ Input_tokens int }
	if code != 200 || json.Unmarshal([]byte(body), &r) != nil || r.Input_tokens < 100 || r.Input_tokens > 1000 || hdr.Get("x-aituner-estimate") == "" {
		t.Fatalf("%d %s %v", code, body, hdr)
	}
}

func TestKeyFileIsPrivateStableAndRegenerated(t *testing.T) {
	p := t.TempDir() + "/gateway.key"
	k1, err := LoadOrCreateKey(p)
	if err != nil || !strings.HasPrefix(k1, "aituner-") || len(k1) < 30 {
		t.Fatalf("%q %v", k1, err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", fi.Mode().Perm())
	}
	if k2, _ := LoadOrCreateKey(p); k2 != k1 {
		t.Fatal("the key must be stable across restarts")
	}
	os.WriteFile(p, []byte("short"), 0o600) // a corrupt/short key is replaced, never used
	if k3, _ := LoadOrCreateKey(p); k3 == "short" || len(k3) < 30 {
		t.Fatalf("weak key kept: %q", k3)
	}
	if NewKey() == NewKey() {
		t.Fatal("keys must be random")
	}
}

func FuzzGateway(f *testing.F) {
	f.Add([]byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"a","input_schema":{}}]}`), "```json\n{\"name\":\"a\",\"arguments\":{}}\n```")
	f.Add([]byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"x","name":"a","input":null}]}]}`), "<tool_call>{\"name\":\"a\"")
	f.Fuzz(func(t *testing.T, req []byte, text string) {
		if tr, err := ToOpenAI(req); err == nil {
			if _, e := json.Marshal(tr.Body); e != nil {
				t.Fatalf("translated body is not JSON: %v", e)
			}
		}
		prose, calls := ExtractToolCalls(text, ToolSet{"a": nil})
		for _, c := range calls {
			var o map[string]any
			if c.Name != "a" || json.Unmarshal(c.Args, &o) != nil {
				t.Fatalf("extracted a call that is not a declared tool with object args: %+v", c)
			}
		}
		_ = prose
		var buf bytes.Buffer
		_ = StreamToAnthropic(&buf, nil, strings.NewReader("data: "+text+"\n\n"), "m", ToolSet{"a": nil})
		if b, _, err := sanitize(req, allowedChat); err == nil {
			var m map[string]any
			if json.Unmarshal(b, &m) != nil || m["model"] != "default_model" {
				t.Fatalf("sanitize let a model through: %s", b)
			}
		}
	})
}

func streamOf(t *testing.T, chunks ...string) (string, []map[string]string, string) {
	t.Helper()
	var sse strings.Builder
	for i, c := range chunks {
		fin := ""
		if i == len(chunks)-1 {
			fin = "stop"
		}
		b, _ := json.Marshal(c)
		sse.WriteString(chunk(string(b), fin))
	}
	sse.WriteString("data: [DONE]\n\n")
	var buf bytes.Buffer
	if err := StreamToAnthropic(&buf, nil, strings.NewReader(sse.String()), "m", weather); err != nil {
		t.Fatal(err)
	}
	text, tools, stop, _, _ := checkStream(t, parseSSE(t, buf.String()))
	return text, tools, stop
}

func TestStreamMidMessageFencedToolCallAndLegitimateJSONBlocks(t *testing.T) {
	// prose, then a fenced tool call split across chunks: prose shown, call extracted, no fence leaks
	text, tools, stop := streamOf(t, "Let me check.\n", "```js", "on\n{\"name\":\"get_weather\",", "\"arguments\":{\"city\":\"Oslo\"}}\n", "```")
	if len(tools) != 1 || stop != "tool_use" || strings.Contains(text, "```") || !strings.HasPrefix(text, "Let me check.") {
		t.Fatalf("text=%q tools=%v stop=%s", text, tools, stop)
	}
	// a JSON block that is an ordinary example (not a declared tool) must be shown in full
	text, tools, stop = streamOf(t, "Here is the config:\n```json\n", "{\"name\":\"server\",\"port\":8080}\n```\n", "That is all.")
	want := "Here is the config:\n```json\n{\"name\":\"server\",\"port\":8080}\n```\nThat is all."
	if len(tools) != 0 || stop != "end_turn" || text != want {
		t.Fatalf("legitimate JSON block was altered:\n got %q\nwant %q (tools=%v)", text, want, tools)
	}
	// marker split at every possible position still resolves
	full := "<tool_call>{\"name\":\"get_weather\",\"arguments\":{\"city\":\"Rome\"}}</tool_call>"
	for cut := 1; cut < len(full); cut += 7 {
		if _, tools, _ = streamOf(t, "ok ", full[:cut], full[cut:]); len(tools) != 1 {
			t.Fatalf("split at %d: %v", cut, tools)
		}
	}
}

// Claude Code 2.1.x sends system-role entries inside "messages" (found by running the real client).
func TestSystemRoleMessagesAreMergedIntoOneLeadingSystemMessage(t *testing.T) {
	tr, err := ToOpenAI([]byte(`{"system":"Base rules.","messages":[{"role":"user","content":"hi"},{"role":"system","content":[{"type":"text","text":"Extra reminder."}]},{"role":"assistant","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	m := tr.Body["messages"].([]map[string]any)
	if len(m) != 3 || m[0]["role"] != "system" || m[0]["content"] != "Base rules.\n\nExtra reminder." || m[1]["role"] != "user" || m[2]["role"] != "assistant" {
		t.Fatalf("%v", m)
	}
	for _, x := range m[1:] {
		if x["role"] == "system" {
			t.Fatal("a second system message would break most chat templates")
		}
	}
}

// Found by running the generated Claude launcher: the gateway was created before any model was started and reported a
// placeholder name and no context window for the rest of its life.
func TestModelsListingReflectsTheModelStartedAfterTheGatewayWasCreated(t *testing.T) {
	g, srv := newGW(t, nil)
	g.ModelID = ""
	repo, window := "", 0
	g.Info = func() (string, int) { return repo, window }
	get := func() (string, float64) {
		_, body, _ := call(t, srv, "GET", "/v1/models", "", bearer)
		var r struct {
			Data []struct {
				ID string  `json:"id"`
				CW float64 `json:"context_window"`
			}
		}
		if err := json.Unmarshal([]byte(body), &r); err != nil || len(r.Data) != 1 {
			t.Fatalf("%v %s", err, body)
		}
		return r.Data[0].ID, r.Data[0].CW
	}
	if id, cw := get(); id != "aituner-local" || cw != 0 {
		t.Fatalf("before any model: %q %v", id, cw)
	}
	repo, window = "mlx-community/Late-4bit", 131072 // the model starts later
	if id, cw := get(); id != "mlx-community/Late-4bit" || cw != 131072 {
		t.Fatalf("after the model started: %q %v", id, cw)
	}
	repo, window = "other/Model-8bit", 32768 // and can change again
	if id, cw := get(); id != "other/Model-8bit" || cw != 32768 {
		t.Fatalf("after switching models: %q %v", id, cw)
	}
}

// Security review: the streaming filter must stay bounded and fast whatever it is fed.
func TestFilterIsBoundedAndFastOnAdversarialStreams(t *testing.T) {
	start := time.Now()
	// 1) endless text that looks like the start of a JSON tool call, in tiny chunks
	f := newFilter(weather)
	var shown strings.Builder
	shown.WriteString(f.Push(`{"name":"get_weather","arguments":{"a":`))
	for i := 0; i < 200_000; i++ {
		shown.WriteString(f.Push("x,"))
	}
	text, calls := f.Finish()
	shown.WriteString(text)
	if len(calls) != 0 || shown.Len() < 400_000 {
		t.Fatalf("held text must be released, not lost: shown=%d calls=%d", shown.Len(), len(calls))
	}
	// 2) a runaway tool-call body
	f = newFilter(weather)
	f.Push("<tool_call>")
	total := 0
	for i := 0; i < 1_500_000; i++ {
		total += len(f.Push("0123456789")) // 15 MB in 10-byte chunks
	}
	text, _ = f.Finish()
	if total+len(text) < 15_000_000-len("<tool_call>") {
		t.Fatalf("no output may be lost when the limit is hit: %d", total+len(text))
	}
	// 3) a huge fenced block that never closes
	f = newFilter(weather)
	var out strings.Builder
	out.WriteString(f.Push("intro\n```json\n"))
	for i := 0; i < 100_000; i++ {
		out.WriteString(f.Push("{\"k\":1}\n"))
	}
	rest, _ := f.Finish()
	if out.Len()+len(rest) < 800_000 {
		t.Fatalf("fenced block lost: %d", out.Len()+len(rest))
	}
	limit := 3 * time.Second
	if raceEnabled {
		limit = 15 * time.Second // the race detector slows tight loops roughly tenfold
	}
	if d := time.Since(start); d > limit {
		t.Fatalf("filter took %v on adversarial input: it is not bounded", d)
	}
}

func TestChatTemplateKwargsAreRestrictedToFlatScalars(t *testing.T) {
	cases := map[string]string{
		`{"chat_template_kwargs":{"enable_thinking":false,"style":"terse","n":3}}`:    `{"enable_thinking":false,"n":3,"style":"terse"}`,
		`{"chat_template_kwargs":{"nested":{"a":1},"list":[1,2],"ok":true}}`:          `{"ok":true}`,
		`{"chat_template_kwargs":{"nested":{"a":1}}}`:                                 "",
		`{"chat_template_kwargs":"not an object"}`:                                    "",
		`{"chat_template_kwargs":{}}`:                                                 "",
		`{"chat_template_kwargs":{"long":"` + strings.Repeat("a", 300) + `","ok":1}}`: `{"ok":1}`,
	}
	for in, want := range cases {
		b, _, err := sanitize([]byte(in), allowedChat)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		json.Unmarshal(b, &m)
		got := string(m["chat_template_kwargs"])
		if got != want {
			t.Errorf("%.60s...\\n got %q\\nwant %q", in, got, want)
		}
	}
	big := `{"chat_template_kwargs":{`
	for i := 0; i < 40; i++ {
		big += fmt.Sprintf(`"k%d":1,`, i)
	}
	big += `"z":1}}`
	if b, _, _ := sanitize([]byte(big), allowedChat); strings.Contains(string(b), "chat_template_kwargs") {
		t.Error("more than 16 keys must be dropped")
	}
}

func TestParallelSemicolonSeparatedCallsAreExtracted(t *testing.T) {
	tools := ToolSet{"Bash": nil, "Read": nil}
	text := `{"name": "Bash", "parameters": {"command": "ls"}}; {"name": "Read", "parameters": {"file_path": "/a"}}`
	prose, calls := ExtractToolCalls(text, tools)
	if prose != "" || len(calls) != 2 || calls[0].Name != "Bash" || calls[1].Name != "Read" {
		t.Fatalf("prose=%q calls=%+v", prose, calls)
	}
	// with the python tag Llama uses, and streamed one character at a time
	f := newFilter(tools)
	var shown string
	for _, r := range "<|python_tag|>" + text {
		shown += f.Push(string(r))
	}
	rest, got := f.Finish()
	if shown+rest != "" || len(got) != 2 {
		t.Fatalf("stream leaked %q / %q calls=%d", shown, rest, len(got))
	}
	// JSON followed by prose is still prose, and an unknown tool is never run
	if _, c := ExtractToolCalls(`{"name":"Bash","parameters":{}} and then I will`, tools); len(c) != 0 {
		t.Fatal("prose after JSON was treated as a call")
	}
	if _, c := ExtractToolCalls(`{"name":"Rm","parameters":{}}; {"name":"Bash","parameters":{}}`, tools); len(c) != 0 {
		t.Fatal("a sequence with an undeclared tool must not run partially")
	}
}

func TestRepairFitsArgumentsToTheSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"number"},"background":{"type":"boolean"},"limit":{"type":"integer"},"path":{"type":"string"}},"required":["command"]}`)
	ts := ToolSet{"Bash": schema}
	got := string(ts.Repair("Bash", json.RawMessage(`{"command":"ls","timeout":0,"background":"False","limit":"10","path":"","bogus":1}`)))
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatal(err)
	}
	if m["command"] != "ls" || m["background"] != false || m["limit"] != float64(10) {
		t.Fatalf("not coerced: %s", got)
	}
	for _, k := range []string{"timeout", "path", "bogus"} {
		if _, ok := m[k]; ok {
			t.Fatalf("%s should have been dropped: %s", k, got)
		}
	}
	// a required argument is never dropped, even when it looks like a placeholder
	if got := string(ts.Repair("Bash", json.RawMessage(`{"command":""}`))); got != `{"command":""}` {
		t.Fatalf("required argument dropped: %s", got)
	}
	// no schema, or arguments that are not an object: untouched
	if got := string(ToolSet{"X": nil}.Repair("X", json.RawMessage(`{"a":0}`))); got != `{"a":0}` {
		t.Fatalf("%s", got)
	}
}

func TestSeveralToolCallsInOneTurnAreSentOnePerMessage(t *testing.T) {
	body := `{"model":"m","max_tokens":10,"tools":[{"name":"Bash","input_schema":{"type":"object"}}],"messages":[
	 {"role":"user","content":"go"},
	 {"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","id":"a","name":"Bash","input":{"command":"ls"}},{"type":"tool_use","id":"b","name":"Bash","input":{"command":"pwd"}}]},
	 {"role":"user","content":[{"type":"tool_result","tool_use_id":"b","content":"/tmp"},{"type":"tool_result","tool_use_id":"a","content":"x"}]}]}`
	tr, err := ToOpenAI([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	msgs := tr.Body["messages"].([]map[string]any)
	var seq []string
	for _, m := range msgs {
		id := ""
		if c, ok := m["tool_calls"].([]map[string]any); ok {
			if len(c) != 1 {
				t.Fatalf("assistant message with %d calls", len(c))
			}
			id = c[0]["id"].(string)
		} else if v, ok := m["tool_call_id"].(string); ok {
			id = v
		}
		seq = append(seq, m["role"].(string)+":"+id)
	}
	if got := strings.Join(seq, " "); got != "user: assistant:a tool:a assistant:b tool:b" {
		t.Fatalf("order: %s", got)
	}
	if msgs[1]["content"] != "on it" || msgs[3]["content"] != "" {
		t.Fatalf("text belongs to the first call only: %v / %v", msgs[1]["content"], msgs[3]["content"])
	}
}

func TestQwenCoderXMLAndHarmonyFormats(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"limit":{"type":"integer"},"edits":{"type":"array"}},"required":["file_path"]}`)
	tools := ToolSet{"Read": schema}
	xml := "I will read it.\n<tool_call>\n<function=Read>\n<parameter=file_path>\n/tmp/a.py\n</parameter>\n<parameter=limit>\n20\n</parameter>\n<parameter=edits>\n[1,2]\n</parameter>\n</function>\n</tool_call>"
	prose, calls := ExtractToolCalls(xml, tools)
	if prose != "I will read it." || len(calls) != 1 || calls[0].Name != "Read" {
		t.Fatalf("prose=%q calls=%+v", prose, calls)
	}
	var a map[string]any
	json.Unmarshal(calls[0].Args, &a)
	if a["file_path"] != "/tmp/a.py" || a["limit"] != float64(20) || len(a["edits"].([]any)) != 2 {
		t.Fatalf("args: %s", calls[0].Args)
	}
	// undeclared functions are prose, and a streamed call never leaks its markup
	if _, c := ExtractToolCalls("<function=Rm>\n<parameter=x>1</parameter>\n</function>", tools); len(c) != 0 {
		t.Fatal("undeclared tool ran")
	}
	f := newFilter(tools)
	var shown string
	for _, r := range xml {
		shown += f.Push(string(r))
	}
	rest, got := f.Finish()
	if strings.Contains(shown+rest, "<function") || len(got) != 1 {
		t.Fatalf("leaked %q / %q calls=%d", shown, rest, len(got))
	}

	// harmony: only the final channel reaches the user; a commentary call becomes a tool call
	h := "<|channel|>analysis<|message|>thinking...<|end|><|start|>assistant<|channel|>final<|message|>There are 4 files.<|return|>"
	if prose, c := ExtractToolCalls(h, tools); prose != "There are 4 files." || len(c) != 0 {
		t.Fatalf("final: %q %+v", prose, c)
	}
	hc := `<|channel|>analysis<|message|>need to read<|end|><|start|>assistant<|channel|>commentary to=functions.Read <|constrain|>json<|message|>{"file_path":"/a"}<|call|>`
	if prose, c := ExtractToolCalls(hc, tools); prose != "" || len(c) != 1 || c[0].Name != "Read" {
		t.Fatalf("call: %q %+v", prose, c)
	}
}

func TestHarmonyReplyStreamsAsItsFinalChannelOnly(t *testing.T) {
	f := newFilter(ToolSet{"Read": nil})
	var shown string
	for _, r := range "<|channel|>analysis<|message|>hm<|start|>assistant<|channel|>final<|message|>Hello there." {
		shown += f.Push(string(r))
	}
	rest, calls := f.Finish()
	if shown+rest != "Hello there." || len(calls) != 0 {
		t.Fatalf("shown=%q rest=%q", shown, rest)
	}
}
