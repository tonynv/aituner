package gateway

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// ToolSet is the tools a request declares: name to JSON input schema. Only declared tools may be extracted from model
// text as tool calls, and their arguments are repaired against the schema (small models get types and optional
// arguments wrong: "False" for a boolean, 0 for a timeout, keys the tool does not have).
type ToolSet map[string]json.RawMessage

func (t ToolSet) Has(name string) bool { _, ok := t[name]; return ok }

type propSchema struct {
	Type json.RawMessage `json:"type"`
}

// Repair returns args made to fit tool's input schema. It never invents values: it drops keys the schema does not
// list, drops optional arguments given a placeholder (null, "", 0), and converts
// a value to the declared scalar type when that is unambiguous. Arguments that cannot be understood are returned as is.
func (t ToolSet) Repair(tool string, args json.RawMessage) json.RawMessage {
	schema := t[tool]
	if len(bytes.TrimSpace(schema)) == 0 {
		return args
	}
	var sc struct {
		Properties map[string]propSchema `json:"properties"`
		Required   []string              `json:"required"`
	}
	var in map[string]json.RawMessage
	if json.Unmarshal(schema, &sc) != nil || len(sc.Properties) == 0 || json.Unmarshal(args, &in) != nil {
		return args
	}
	required := map[string]bool{}
	for _, r := range sc.Required {
		required[r] = true
	}
	out := map[string]json.RawMessage{}
	for k, v := range in {
		p, known := sc.Properties[k]
		if !known {
			continue
		}
		typ := scalarType(p.Type)
		val := coerce(v, typ)
		if !required[k] && placeholder(val, typ) {
			continue
		}
		out[k] = val
	}
	b, err := json.Marshal(out)
	if err != nil {
		return args
	}
	return b
}

// scalarType is the single JSON type a property declares ("" when it is a union or not a scalar).
func scalarType(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func coerce(v json.RawMessage, typ string) json.RawMessage {
	var s string
	isStr := json.Unmarshal(v, &s) == nil
	switch typ {
	case "boolean":
		if isStr {
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "true":
				return json.RawMessage("true")
			case "false":
				return json.RawMessage("false")
			}
		}
	case "integer":
		if isStr {
			if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
				return json.RawMessage(strconv.FormatInt(n, 10))
			}
		}
	case "number":
		if isStr {
			if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
				return json.RawMessage(strconv.FormatFloat(f, 'f', -1, 64))
			}
		}
	case "string":
		if !isStr {
			var n json.Number
			if json.Unmarshal(v, &n) == nil {
				return json.RawMessage(strconv.Quote(n.String()))
			}
		}
	}
	return v
}

// placeholder reports values a model writes for "not set": null, an empty string, zero for a number. (false is kept: a
// flag may default to true.)
func placeholder(v json.RawMessage, typ string) bool {
	t := strings.TrimSpace(string(v))
	switch t {
	case "null", `""`:
		return true
	case "0":
		return typ == "integer" || typ == "number"
	}
	return false
}
