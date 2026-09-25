package gateway

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ---- Anthropic Messages request -> OpenAI chat completions request ---------------------------------------------

type aMsgReq struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        json.RawMessage `json:"system"`
	Messages      []aMsg          `json:"messages"`
	Temperature   *float64        `json:"temperature"`
	TopP          *float64        `json:"top_p"`
	TopK          *int            `json:"top_k"`
	StopSequences []string        `json:"stop_sequences"`
	Stream        bool            `json:"stream"`
	Tools         []aTool         `json:"tools"`
	ToolChoice    json.RawMessage `json:"tool_choice"`
}

type aMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []block
}

type aTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type aBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// blocksOf normalises "content" (a string or an array of blocks) into blocks.
func blocksOf(raw json.RawMessage) ([]aBlock, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return []aBlock{{Type: "text", Text: s}}, nil
	}
	var bs []aBlock
	if err := json.Unmarshal(raw, &bs); err != nil {
		return nil, fmt.Errorf("content must be a string or an array of blocks")
	}
	return bs, nil
}

func textOf(blocks []aBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

const imageNote = "[an image was attached here, but this local model is text-only]"

// Translated is an Anthropic request converted for the model server.
type Translated struct {
	Body   map[string]any
	Model  string // the model name the client asked for, echoed back in responses
	Stream bool
	Tools  map[string]bool // declared tool names: only these may be extracted from model text as tool calls
}

// ToOpenAI converts an Anthropic Messages request into an OpenAI chat-completions body.
func ToOpenAI(raw []byte) (Translated, error) {
	body, model, stream, names, err := toOpenAI(raw)
	return Translated{Body: body, Model: model, Stream: stream, Tools: names}, err
}

func toOpenAI(raw []byte) (map[string]any, string, bool, map[string]bool, error) {
	var req aMsgReq
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, "", false, nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	if len(req.Messages) == 0 {
		return nil, "", false, nil, fmt.Errorf("messages must not be empty")
	}
	var msgs []map[string]any
	var sysParts []string // the system prompt, plus any system-role messages (Claude Code sends some inside messages)
	if sb, err := blocksOf(req.System); err != nil {
		return nil, "", false, nil, fmt.Errorf("system: %w", err)
	} else if t := textOf(sb); t != "" {
		sysParts = append(sysParts, t)
	}
	for i, m := range req.Messages {
		blocks, err := blocksOf(m.Content)
		if err != nil {
			return nil, "", false, nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		switch m.Role {
		case "system":
			if t := textOf(blocks); t != "" {
				sysParts = append(sysParts, t)
			}
		case "user":
			var tools []map[string]any
			var parts []string
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if b.Text != "" {
						parts = append(parts, b.Text)
					}
				case "image":
					parts = append(parts, imageNote)
				case "tool_result":
					cb, _ := blocksOf(b.Content)
					content := textOf(cb)
					if b.IsError {
						content = "Error: " + content
					}
					tools = append(tools, map[string]any{"role": "tool", "tool_call_id": b.ToolUseID, "content": content})
				}
			}
			// OpenAI requires tool results to directly follow the assistant tool call, so they come first.
			for _, t := range tools {
				msgs = append(msgs, t)
			}
			if len(parts) > 0 {
				msgs = append(msgs, map[string]any{"role": "user", "content": strings.Join(parts, "\n\n")})
			}
		case "assistant":
			var calls []map[string]any
			for _, b := range blocks {
				if b.Type == "tool_use" {
					args := string(b.Input)
					if strings.TrimSpace(args) == "" {
						args = "{}"
					}
					calls = append(calls, map[string]any{"id": b.ID, "type": "function", "function": map[string]any{"name": b.Name, "arguments": args}})
				}
			}
			msg := map[string]any{"role": "assistant", "content": textOf(blocks)} // thinking blocks are dropped
			if len(calls) > 0 {
				msg["tool_calls"] = calls
			}
			msgs = append(msgs, msg)
		default:
			return nil, "", false, nil, fmt.Errorf("messages[%d]: unsupported role %q", i, m.Role)
		}
	}
	if len(msgs) == 0 { // a system prompt alone is not a conversation
		return nil, "", false, nil, fmt.Errorf("messages must contain at least one user or assistant message")
	}
	if len(sysParts) > 0 { // chat templates want exactly one system message, first
		msgs = append([]map[string]any{{"role": "system", "content": strings.Join(sysParts, "\n\n")}}, msgs...)
	}
	out := map[string]any{"messages": msgs, "max_tokens": req.MaxTokens}
	if req.MaxTokens <= 0 {
		out["max_tokens"] = 4096
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if req.TopK != nil {
		out["top_k"] = *req.TopK
	}
	if len(req.StopSequences) > 0 {
		out["stop"] = req.StopSequences
	}
	var tools []map[string]any
	names := map[string]bool{}
	for _, t := range req.Tools {
		if len(t.InputSchema) == 0 { // server-side tools (web search etc.) have no schema and cannot run locally
			continue
		}
		names[t.Name] = true
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": json.RawMessage(t.InputSchema)}})
	}
	if len(tools) > 0 {
		out["tools"] = tools
		var tc struct{ Type, Name string }
		_ = json.Unmarshal(req.ToolChoice, &tc)
		switch tc.Type {
		case "any":
			out["tool_choice"] = "required"
		case "tool":
			out["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": tc.Name}}
		case "none":
			out["tool_choice"] = "none"
		default:
			out["tool_choice"] = "auto"
		}
	}
	if req.Stream {
		out["stream"] = true
		out["stream_options"] = map[string]any{"include_usage": true}
	}
	return out, req.Model, req.Stream, names, nil
}

// ---- OpenAI response -> Anthropic response ---------------------------------------------------------------------

func stopReason(finish string) string {
	switch finish {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}

func newID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// inputJSON turns a tool call's arguments (a JSON string, or already an object) into a JSON object.
func inputJSON(args json.RawMessage) json.RawMessage {
	s := strings.TrimSpace(string(args))
	if s == "" || s == "null" {
		return json.RawMessage("{}")
	}
	if s[0] == '"' { // a JSON string holding JSON
		var inner string
		if json.Unmarshal(args, &inner) == nil {
			s = strings.TrimSpace(inner)
		}
	}
	if s == "" {
		return json.RawMessage("{}")
	}
	var probe map[string]any
	if json.Unmarshal([]byte(s), &probe) == nil {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(map[string]string{"raw_arguments": s})
	return b
}

type oaResp struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content   *string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// FromOpenAI converts a non-streaming OpenAI response into an Anthropic message.
func FromOpenAI(raw []byte, model string, names map[string]bool) (map[string]any, error) {
	var r oaResp
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Choices) == 0 {
		return nil, fmt.Errorf("unexpected response from the model server")
	}
	c := r.Choices[0]
	content := []map[string]any{}
	text := ""
	if c.Message.Content != nil {
		text = *c.Message.Content
	}
	var extracted []Call
	if len(c.Message.ToolCalls) == 0 { // the server did not parse a tool call: look for one in the text
		text, extracted = ExtractToolCalls(text, names)
	} else {
		text = strings.TrimSpace(stripSpecial(text))
	}
	if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, ec := range extracted {
		content = append(content, map[string]any{"type": "tool_use", "id": newID("toolu_"), "name": ec.Name, "input": ec.Args})
	}
	for _, tc := range c.Message.ToolCalls {
		id := tc.ID
		if id == "" {
			id = newID("toolu_")
		}
		content = append(content, map[string]any{"type": "tool_use", "id": id, "name": tc.Function.Name, "input": inputJSON(tc.Function.Arguments)})
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}
	reason := stopReason(c.FinishReason)
	if len(c.Message.ToolCalls) > 0 || len(extracted) > 0 {
		reason = "tool_use"
	}
	return map[string]any{
		"id": newID("msg_"), "type": "message", "role": "assistant", "model": model, "content": content,
		"stop_reason": reason, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": r.Usage.PromptTokens, "output_tokens": r.Usage.CompletionTokens},
	}, nil
}

// ---- streaming: OpenAI SSE -> Anthropic SSE --------------------------------------------------------------------

type oaChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type sseWriter struct {
	w io.Writer
	f http.Flusher
}

func (s sseWriter) event(name string, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, b)
	if s.f != nil {
		s.f.Flush()
	}
}

// argsFragment returns a tool-call arguments delta as text (the server may stream a string or send a whole object).
func argsFragment(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	if s[0] == '"' {
		var t string
		if json.Unmarshal(raw, &t) == nil {
			return t
		}
	}
	return s
}

// StreamToAnthropic reads an OpenAI chat-completions SSE stream and writes the equivalent Anthropic Messages SSE
// event sequence: message_start, content blocks (text and tool_use with input_json_delta), message_delta, message_stop.
// When the request declared tools, text that might be a tool call is held back until it is known to be one or not.
func StreamToAnthropic(w io.Writer, flusher http.Flusher, upstream io.Reader, model string, names map[string]bool) error {
	out := sseWriter{w, flusher}
	id := newID("msg_")
	started, textOpen := false, false
	block := -1
	toolBlock := map[int]int{}
	finish := ""
	inTok, outTok := 0, 0
	sawTool := false

	start := func() {
		if started {
			return
		}
		started = true
		out.event("message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": inTok, "output_tokens": 1}}})
	}
	open := -1 // index of the currently open content block, -1 = none
	closeCurrent := func() {
		if open >= 0 {
			out.event("content_block_stop", map[string]any{"type": "content_block_stop", "index": open})
			open, textOpen = -1, false
		}
	}
	emitText := func(t string) {
		if t == "" {
			return
		}
		if !textOpen {
			closeCurrent()
			block++
			open, textOpen = block, true
			out.event("content_block_start", map[string]any{"type": "content_block_start", "index": block, "content_block": map[string]any{"type": "text", "text": ""}})
		}
		out.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": block, "delta": map[string]any{"type": "text_delta", "text": t}})
	}
	emitTool := func(tid, name, args string) int {
		closeCurrent()
		block++
		if tid == "" {
			tid = newID("toolu_")
		}
		out.event("content_block_start", map[string]any{"type": "content_block_start", "index": block, "content_block": map[string]any{"type": "tool_use", "id": tid, "name": name, "input": map[string]any{}}})
		open = block
		if args != "" {
			out.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": block, "delta": map[string]any{"type": "input_json_delta", "partial_json": args}})
		}
		sawTool = true
		return block
	}

	filt := newFilter(names) // holds back only text that might be a tool call
	onText := func(d string) { emitText(filt.Push(d)) }

	sc := bufio.NewScanner(upstream)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var ch oaChunk
		if json.Unmarshal([]byte(data), &ch) != nil {
			continue
		}
		if ch.Usage != nil {
			inTok, outTok = ch.Usage.PromptTokens, ch.Usage.CompletionTokens
		}
		start()
		for _, c := range ch.Choices {
			if d := stripSpecial(c.Delta.Content); d != "" {
				onText(d)
			}
			for _, tc := range c.Delta.ToolCalls { // structured tool calls from the server
				bi, known := toolBlock[tc.Index]
				if !known {
					bi = emitTool(tc.ID, tc.Function.Name, "")
					toolBlock[tc.Index] = bi
				}
				if frag := argsFragment(tc.Function.Arguments); frag != "" {
					out.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": bi, "delta": map[string]any{"type": "input_json_delta", "partial_json": frag}})
				}
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				finish = *c.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		closeCurrent()
		out.event("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": "the model server stream broke: " + err.Error()}})
		return err
	}
	if !started {
		out.event("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": "the model server returned no output"}})
		return fmt.Errorf("empty upstream stream")
	}
	{ // the message ended: release whatever was still held, as text or as tool calls
		text, calls := filt.Finish()
		emitText(text)
		for _, c := range calls {
			emitTool("", c.Name, string(c.Args))
		}
	}
	closeCurrent()
	reason := stopReason(finish)
	if sawTool {
		reason = "tool_use"
	}
	out.event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": reason, "stop_sequence": nil},
		"usage": map[string]any{"input_tokens": inTok, "output_tokens": outTok}})
	out.event("message_stop", map[string]any{"type": "message_stop"})
	return nil
}

// AnthropicError is the error envelope Anthropic clients expect.
func AnthropicError(kind, msg string) map[string]any {
	return map[string]any{"type": "error", "error": map[string]any{"type": kind, "message": msg}}
}
