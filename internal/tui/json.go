package tui

import (
	"bytes"
	"encoding/json"
	"strings"
)

// renderJSON shows a text that is one JSON object or array as indented
// "key: value" lines, in the order it came. A string with line breaks gets
// its own lines. It reports false when the text is not JSON.
func renderJSON(text string, w int, th *Theme, prefix []Span) ([]Line, bool) {
	t := strings.TrimSpace(text)
	if t == "" || t[0] != '{' && t[0] != '[' || !json.Valid([]byte(t)) {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(t))
	dec.UseNumber()
	var out []Line
	indent := func(depth int) []Span {
		return append(clone(prefix), Span{strings.Repeat("  ", depth), th.Text})
	}
	var value func(key []Span, depth int)
	value = func(key []Span, depth int) {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		switch v := tok.(type) {
		case json.Delim:
			if len(key) > 0 {
				out = append(out, Line{Prefix: indent(depth), Spans: key})
				depth++
			}
			for dec.More() {
				var k []Span
				if v == '{' {
					name, _ := dec.Token()
					k = []Span{{name.(string) + ":", th.Foam}}
				} else {
					k = []Span{{"-", th.Muted}}
				}
				value(k, depth)
			}
			_, _ = dec.Token()
		case string:
			if strings.Contains(v, "\n") {
				out = append(out, Line{Prefix: indent(depth), Spans: key})
				for _, l := range strings.Split(strings.TrimRight(v, "\n"), "\n") {
					// wrap drops leading spaces; the line's own indent goes
					// into the prefix.
					body := strings.TrimLeft(l, " ")
					in := append(indent(depth+1), Span{l[:len(l)-len(body)], th.Text})
					out = append(out, wrap([]Span{{body, th.Text}}, w, in, in)...)
				}
				return
			}
			out = append(out, wrap(append(clone(key), Span{" " + v, th.Text}), w, indent(depth), indent(depth+1))...)
		default:
			var b bytes.Buffer
			_ = json.NewEncoder(&b).Encode(v)
			out = append(out, wrap(append(clone(key), Span{" " + strings.TrimSpace(b.String()), th.Iris}), w, indent(depth), indent(depth+1))...)
		}
	}
	value(nil, 0)
	return out, true
}
