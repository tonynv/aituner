package gateway

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Two more text formats local models use, beyond the JSON ones in toolparse.go:
//
//   - Qwen3-Coder's XML calls:  <tool_call><function=Read><parameter=file_path>/a.py</parameter></function></tool_call>
//   - gpt-oss "harmony" channels: <|channel|>analysis<|message|>...<|end|><|start|>assistant<|channel|>final<|message|>Hi<|return|>
//     with calls as <|channel|>commentary to=functions.Read <|constrain|>json<|message|>{...}<|call|>

var (
	fnRe    = regexp.MustCompile(`(?s)(?:<tool_call>\s*)?<function=([A-Za-z0-9_.\-]+)>(.*?)(?:</function>|\z)(?:\s*</tool_call>)?`)
	paramRe = regexp.MustCompile(`(?s)<parameter=([A-Za-z0-9_.\-]+)>\n?(.*?)\n?(?:</parameter>|\z)`)

	harmonyCallRe  = regexp.MustCompile(`(?s)<\|channel\|>commentary to=functions\.([A-Za-z0-9_.\-]+)[^<]*(?:<\|constrain\|>[A-Za-z0-9_\-]+)?<\|message\|>(.*?)(?:<\|call\|>|\z)`)
	harmonyFinalRe = regexp.MustCompile(`(?s)<\|channel\|>final<\|message\|>(.*?)(?:<\|return\|>|<\|end\|>|<\|start\|>|<\|channel\|>|\z)`)
	harmonyNoteRe  = regexp.MustCompile(`(?s)<\|channel\|>commentary<\|message\|>(.*?)(?:<\|end\|>|<\|start\|>|<\|channel\|>|\z)`)
)

// extractFunctionXML pulls declared-tool calls in Qwen3-Coder's XML form out of text and returns the rest.
func extractFunctionXML(text string, names ToolSet) (string, []Call) {
	var calls []Call
	var out strings.Builder
	last := 0
	for _, m := range fnRe.FindAllStringSubmatchIndex(text, -1) {
		name := text[m[2]:m[3]]
		if !names.Has(name) {
			continue // ordinary text that happens to look like a call: left alone
		}
		args := map[string]json.RawMessage{}
		for _, p := range paramRe.FindAllStringSubmatch(text[m[4]:m[5]], -1) {
			b, _ := json.Marshal(p[2])
			args[p[1]] = b
		}
		raw, _ := json.Marshal(args)
		calls = append(calls, Call{Name: name, Args: names.Repair(name, raw)})
		out.WriteString(text[last:m[0]])
		last = m[1]
	}
	out.WriteString(text[last:])
	return out.String(), calls
}

// extractHarmony returns what a gpt-oss style reply says to the user (the final channel; otherwise the commentary
// preamble) and its declared-tool calls. The analysis channel is the model thinking aloud and is dropped.
func extractHarmony(text string, names ToolSet) (string, []Call) {
	var calls []Call
	for _, m := range harmonyCallRe.FindAllStringSubmatch(text, -1) {
		if !names.Has(m[1]) {
			continue
		}
		body := strings.TrimSpace(m[2])
		raw, _, err := decodeOne(body)
		if err != nil {
			continue
		}
		calls = append(calls, Call{Name: m[1], Args: names.Repair(m[1], inputJSON(raw))})
	}
	var final []string
	for _, m := range harmonyFinalRe.FindAllStringSubmatch(text, -1) {
		final = append(final, m[1])
	}
	if len(final) == 0 && len(calls) == 0 {
		for _, m := range harmonyNoteRe.FindAllStringSubmatch(text, -1) {
			final = append(final, m[1])
		}
	}
	return strings.TrimSpace(strings.Join(final, "\n")), calls
}
