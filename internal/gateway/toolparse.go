package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Local models express tool calls in their own text formats, and mlx_lm.server does not parse every one of them into
// structured tool_calls (measured: Llama 3.1 returns `<|python_tag|>{...}` as plain content; Qwen2.5-Coder returns a
// fenced ```json block). This file recognises the common formats, but ONLY accepts a call whose name is one of the tools
// the client declared, so ordinary JSON in an answer is never mistaken for a tool call.

// Call is one extracted tool call.
type Call struct {
	Name string
	Args json.RawMessage // a JSON object
}

var specialTokens = []string{"<|im_end|>", "<|im_start|>", "<|eot_id|>", "<|eom_id|>", "<|endoftext|>", "<|end|>", "</s>"}

// stripSpecial removes chat-template control tokens a model leaked into its text.
func stripSpecial(s string) string {
	for _, t := range specialTokens {
		s = strings.ReplaceAll(s, t, "")
	}
	return s
}

// decodeOne decodes the first JSON value at the start of s and returns it with the number of bytes consumed.
func decodeOne(s string) (json.RawMessage, int, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, 0, err
	}
	return raw, int(dec.InputOffset()), nil
}

// callsFromJSON accepts {"name","arguments"|"parameters"|"input"}, {"function":{"name","arguments"}} or an array of
// those, and keeps only calls whose name is declared.
func callsFromJSON(raw json.RawMessage, names map[string]bool) []Call {
	raw = json.RawMessage(bytes.TrimSpace(raw))
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '[' {
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) != nil {
			return nil
		}
		var out []Call
		for _, a := range arr {
			out = append(out, callsFromJSON(a, names)...)
		}
		return out
	}
	var o struct {
		Name       string          `json:"name"`
		Arguments  json.RawMessage `json:"arguments"`
		Parameters json.RawMessage `json:"parameters"`
		Input      json.RawMessage `json:"input"`
		Function   *struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return nil
	}
	name, args := o.Name, firstNonEmpty(o.Arguments, o.Parameters, o.Input)
	if name == "" && o.Function != nil {
		name, args = o.Function.Name, o.Function.Arguments
	}
	if !names[name] {
		return nil
	}
	return []Call{{Name: name, Args: inputJSON(args)}}
}

func firstNonEmpty(rs ...json.RawMessage) json.RawMessage {
	for _, r := range rs {
		if s := strings.TrimSpace(string(r)); s != "" && s != "null" {
			return r
		}
	}
	return nil
}

// ExtractToolCalls finds declared-tool calls in text and returns the prose that remains.
func ExtractToolCalls(text string, names map[string]bool) (string, []Call) {
	if len(names) == 0 {
		return strings.TrimSpace(stripSpecial(text)), nil
	}
	text = stripSpecial(text)
	var calls []Call
	cut := func(start, end int) { text = text[:start] + text[end:] }

	// 1. <tool_call> ... </tool_call> (Hermes-style, used by Qwen and others); the closing tag may be missing
	for {
		i := strings.Index(text, "<tool_call>")
		if i < 0 {
			break
		}
		body := text[i+len("<tool_call>"):]
		end := len(body)
		if j := strings.Index(body, "</tool_call>"); j >= 0 {
			end = j
		}
		raw, _, err := decodeOne(strings.TrimSpace(body[:end]))
		got := callsFromJSON(raw, names)
		if err != nil || len(got) == 0 {
			break
		}
		calls = append(calls, got...)
		after := i + len("<tool_call>") + end
		if strings.HasPrefix(text[after:], "</tool_call>") {
			after += len("</tool_call>")
		}
		cut(i, after)
	}
	// 2. <|python_tag|> followed by JSON (Llama 3.x)
	if i := strings.Index(text, "<|python_tag|>"); i >= 0 {
		body := text[i+len("<|python_tag|>"):]
		if raw, n, err := decodeOne(strings.TrimSpace(body)); err == nil {
			if got := callsFromJSON(raw, names); len(got) > 0 {
				calls = append(calls, got...)
				lead := len(body) - len(strings.TrimLeft(body, " \n\t"))
				cut(i, i+len("<|python_tag|>")+lead+n)
			}
		}
	}
	// 3. fenced ```json blocks (or unlabeled fences)
	for from := 0; ; {
		i := strings.Index(text[from:], "```")
		if i < 0 {
			break
		}
		i += from
		rest := text[i+3:]
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			break
		}
		lang := strings.TrimSpace(rest[:nl])
		body := rest[nl+1:]
		j := strings.Index(body, "```")
		if j < 0 {
			break
		}
		if lang == "" || strings.EqualFold(lang, "json") {
			raw, _, err := decodeOne(strings.TrimSpace(body[:j]))
			if got := callsFromJSON(raw, names); err == nil && len(got) > 0 {
				calls = append(calls, got...)
				cut(i, i+3+nl+1+j+3)
				from = i
				continue
			}
		}
		from = i + 3 + nl + 1 + j + 3
	}
	// 4. the whole message is bare JSON
	if t := strings.TrimSpace(text); t != "" && (t[0] == '{' || t[0] == '[') && len(calls) == 0 {
		if raw, n, err := decodeOne(t); err == nil && strings.TrimSpace(t[n:]) == "" {
			if got := callsFromJSON(raw, names); len(got) > 0 {
				calls, text = got, ""
			}
		}
	}
	return strings.TrimSpace(text), calls
}

// ---- streaming: decide, as text arrives, whether it might be a tool call ---------------------------------------

type verdict int

const (
	hold verdict = iota // keep buffering: it might still turn out to be a tool call
	pass                // definitely ordinary text: release what was held and stop holding
	tool                // a tool call: buffer everything until the message ends, then extract
)

var toolMarkers = []string{"<tool_call>", "<|python_tag|>"}

// classify decides from the text so far (leading whitespace ignored). Unrelated text passes immediately, so answers
// stream normally; only text that could be a tool call is held back.
func classify(so_far string, names map[string]bool) verdict {
	t := strings.TrimLeft(so_far, " \n\t")
	if t == "" {
		return hold
	}
	for _, m := range toolMarkers {
		if strings.HasPrefix(t, m) {
			return tool
		}
		if strings.HasPrefix(m, t) {
			return hold
		}
	}
	switch t[0] {
	case '`':
		if !strings.HasPrefix(t, "```") {
			if strings.HasPrefix("```", t) {
				return hold
			}
			return pass
		}
		nl := strings.IndexByte(t, '\n')
		if nl < 0 {
			return hold // the language tag is not complete yet
		}
		if lang := strings.TrimSpace(t[3:nl]); lang != "" && !strings.EqualFold(lang, "json") {
			return pass // ```python, ```go ...: a code answer, never a tool call
		}
		if !strings.Contains(t[nl+1:], "```") {
			return hold
		}
		if _, calls := ExtractToolCalls(t, names); len(calls) > 0 {
			return tool
		}
		return pass
	case '{', '[':
		raw, n, err := decodeOne(t)
		switch {
		case err == nil && strings.TrimSpace(t[n:]) == "":
			if len(callsFromJSON(raw, names)) > 0 {
				return tool
			}
			return pass
		case err == nil:
			return pass // JSON followed by more text: prose
		case errors.Is(err, io.ErrUnexpectedEOF):
			return hold
		default:
			return pass
		}
	}
	return pass
}

// ---- streaming filter: tool calls can start anywhere in a message ("I will check. <tool_call>...") -------------------

const (
	modeUndecided = iota // start of the message: classify it
	modeScan             // ordinary text; watch for a tool-call marker to appear
	modeTool             // a tool call has begun: collect everything until the message ends
)

// scanMarkers begin a tool call mid-message. "```json" is included because Qwen-style models fence their call; a fenced
// block that turns out not to be a declared tool call is released unchanged once its closing fence arrives.
var scanMarkers = []string{"<tool_call>", "<|python_tag|>", "```json"}

func firstMarker(s string) (int, string) {
	best, which := -1, ""
	for _, m := range scanMarkers {
		if i := strings.Index(s, m); i >= 0 && (best < 0 || i < best) {
			best, which = i, m
		}
	}
	return best, which
}

// partialMarkerSuffix is the length of the longest tail of s that could be the start of a marker split across chunks.
func partialMarkerSuffix(s string) int {
	longest := 0
	for _, m := range scanMarkers {
		for n := 1; n < len(m) && n <= len(s); n++ {
			if strings.HasSuffix(s, m[:n]) && n > longest {
				longest = n
			}
		}
	}
	return longest
}

// Limits keep the filter's cost bounded whatever a model (or a client asking for enormous outputs) produces.
const (
	// maxUndecided: text that has looked like a possible tool call for this long is not one (real calls are short);
	// it is released as ordinary text. This also bounds the JSON re-parsing done while undecided.
	maxUndecided = 64 << 10
	// maxToolBuffer bounds how much of one message is collected once a tool call has begun (a large file written through
	// a tool argument fits comfortably); beyond it the text is released as-is instead of growing without limit.
	maxToolBuffer = 8 << 20
)

// filter decides what part of streamed model text is shown and what is a tool call, holding back only text that
// might still turn out to be one.
type filter struct {
	names map[string]bool
	mode  int
	buf   string          // held text while undecided / scanning (bounded by maxUndecided)
	tool  strings.Builder // everything since a tool call began (a Builder: appending is linear, not quadratic)
	off   bool            // a limit was hit: stop looking for tool calls, pass everything through
}

func newFilter(names map[string]bool) *filter {
	f := &filter{names: names}
	if len(names) == 0 {
		f.mode = modeScan // no tools declared: nothing to look for
	}
	return f
}

// cheap reports whether a new piece of text can change the verdict: it must contain a character that ends a JSON value,
// a code fence, a tag or a line. Re-classifying on every few-byte chunk would re-parse the whole held text each time.
func cheap(d string) bool { return strings.ContainsAny(d, "}]`>\n") }

func (f *filter) startTool() {
	f.mode = modeTool
	f.tool.WriteString(f.buf)
	f.buf = ""
}

// Push takes the next piece of model text and returns the text that is safe to show now.
func (f *filter) Push(d string) string {
	if f.off {
		return d
	}
	if f.mode == modeTool {
		f.tool.WriteString(d)
		if f.tool.Len() > maxToolBuffer { // runaway: give up and show what we have
			out := f.tool.String()
			f.tool.Reset()
			f.off = true
			return out
		}
		return ""
	}
	f.buf += d
	if f.mode == modeUndecided {
		if len(f.buf) > maxUndecided {
			out := f.buf
			f.buf, f.off = "", true
			return out
		}
		if len(f.buf) > 48 && !cheap(d) { // nothing in this chunk can finish a call: skip the (costly) re-parse
			return ""
		}
		switch classify(f.buf, f.names) {
		case hold:
			return ""
		case tool:
			f.startTool()
			return ""
		}
		f.mode = modeScan
	}
	if len(f.names) == 0 {
		out := f.buf
		f.buf = ""
		return out
	}
	var out strings.Builder
	for {
		i, m := firstMarker(f.buf)
		if i < 0 {
			keep := partialMarkerSuffix(f.buf)
			out.WriteString(f.buf[:len(f.buf)-keep])
			f.buf = f.buf[len(f.buf)-keep:]
			break
		}
		out.WriteString(f.buf[:i])
		f.buf = f.buf[i:]
		if m != "```json" {
			f.startTool()
			break
		}
		nl := strings.IndexByte(f.buf, '\n')
		if nl < 0 {
			break
		}
		end := strings.Index(f.buf[nl+1:], "```")
		if end < 0 {
			if len(f.buf) > maxUndecided { // a huge fenced block is not a tool call
				out.WriteString(f.buf)
				f.buf, f.off = "", true
			}
			break // the closing fence has not arrived yet
		}
		block := f.buf[:nl+1+end+3]
		if _, calls := ExtractToolCalls(block, f.names); len(calls) > 0 {
			f.startTool()
			break
		}
		out.WriteString(block)
		f.buf = f.buf[len(block):]
	}
	return out.String()
}

// Finish is called when the message ends: it returns any remaining text and the tool calls found.
func (f *filter) Finish() (string, []Call) {
	rest := f.buf
	if f.mode == modeTool {
		rest = f.tool.String()
	}
	if rest == "" {
		return "", nil
	}
	if f.off {
		return rest, nil
	}
	prose, calls := ExtractToolCalls(rest, f.names)
	if len(calls) == 0 {
		return stripSpecial(rest), nil
	}
	return prose, calls
}
