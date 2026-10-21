package output

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// KV is one key/value pair of an ordered object.
type KV struct {
	Key string
	Val any
}

// Obj is an ordered object. Go maps have no stable order, and agents read
// output top to bottom, so every document tts-axi prints is built from Obj.
//
// A value (Val) may be a string, bool, any int or float type, nil, an Obj,
// a []Obj (rendered as a table when every row has the same keys), or a
// []string.
type Obj []KV

// Set appends a key/value pair and returns the extended object, so documents
// can be built with a fluent style: o = o.Set("a", 1).Set("b", 2).
func (o Obj) Set(key string, val any) Obj { return append(o, KV{key, val}) }

// EncodeTOON renders o in TOON (Token-Oriented Object Notation), a compact,
// indentation-based format that costs far fewer tokens than JSON for the
// same data. This is a deliberately tiny encoder: it covers only the shapes
// tts-axi prints (objects, primitive arrays, uniform tables, nested objects).
func EncodeTOON(o Obj) string {
	var b strings.Builder
	writeObj(&b, o, 0)
	return b.String()
}

func writeObj(b *strings.Builder, o Obj, depth int) {
	pad := strings.Repeat("  ", depth)
	for _, kv := range o {
		key := toonKey(kv.Key)
		switch v := kv.Val.(type) {
		case Obj:
			fmt.Fprintf(b, "%s%s:\n", pad, key)
			writeObj(b, v, depth+1)
		case []Obj:
			writeObjArray(b, pad, key, v, depth)
		case []string:
			if len(v) == 0 {
				fmt.Fprintf(b, "%s%s[0]:\n", pad, key)
				continue
			}
			cells := make([]string, len(v))
			for i, s := range v {
				cells[i] = toonScalar(s)
			}
			fmt.Fprintf(b, "%s%s[%d]: %s\n", pad, key, len(v), strings.Join(cells, ","))
		default:
			fmt.Fprintf(b, "%s%s: %s\n", pad, key, toonScalar(v))
		}
	}
}

func writeObjArray(b *strings.Builder, pad, key string, rows []Obj, depth int) {
	if len(rows) == 0 {
		fmt.Fprintf(b, "%s%s[0]:\n", pad, key)
		return
	}
	if cols, ok := tableColumns(rows); ok {
		fmt.Fprintf(b, "%s%s[%d]{%s}:\n", pad, key, len(rows), strings.Join(cols, ","))
		for _, row := range rows {
			cells := make([]string, len(row))
			for i, kv := range row {
				cells[i] = toonScalar(kv.Val)
			}
			fmt.Fprintf(b, "%s  %s\n", pad, strings.Join(cells, ","))
		}
		return
	}
	// Non-uniform rows fall back to the list form.
	fmt.Fprintf(b, "%s%s[%d]:\n", pad, key, len(rows))
	for _, row := range rows {
		var inner strings.Builder
		writeObj(&inner, row, depth+2)
		lines := strings.Split(strings.TrimRight(inner.String(), "\n"), "\n")
		for i, line := range lines {
			if i == 0 {
				fmt.Fprintf(b, "%s  - %s\n", pad, strings.TrimPrefix(line, strings.Repeat("  ", depth+2)))
			} else {
				fmt.Fprintf(b, "%s\n", line)
			}
		}
	}
}

// tableColumns reports whether every row has the same keys and only scalar
// values, which is what TOON's compact tabular form requires.
func tableColumns(rows []Obj) ([]string, bool) {
	cols := make([]string, len(rows[0]))
	for i, kv := range rows[0] {
		cols[i] = toonKey(kv.Key)
	}
	for _, row := range rows {
		if len(row) != len(cols) {
			return nil, false
		}
		for i, kv := range row {
			if toonKey(kv.Key) != cols[i] {
				return nil, false
			}
			switch kv.Val.(type) {
			case Obj, []Obj, []string:
				return nil, false
			}
		}
	}
	return cols, true
}

var (
	plainKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
	numeric  = regexp.MustCompile(`^-?\d+(\.\d+)?([eE][+-]?\d+)?$`)
)

func toonKey(k string) string {
	if plainKey.MatchString(k) {
		return k
	}
	return quote(k)
}

func toonScalar(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case string:
		return toonString(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	default:
		return fmt.Sprint(x)
	}
}

// toonString quotes a string only when leaving it bare would be ambiguous.
func toonString(s string) string {
	switch {
	case s == "", s == "true", s == "false", s == "null",
		strings.TrimSpace(s) != s,
		numeric.MatchString(s),
		strings.HasPrefix(s, "-"),
		strings.ContainsAny(s, ",:\"\\[]{}\n\r\t"):
		return quote(s)
	}
	return s
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}
