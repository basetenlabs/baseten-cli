package help

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/invopop/jsonschema"
)

// composeExamples produces the newline-joined example block for a leaf's
// help. Examples come first (each prefixed with a `# Description` line),
// followed by JQExample. Returns "" for non-leaves or for leaves with no
// declared examples (e.g. DisableFlagParsing commands).
func composeExamples(spec cmd.CommandOutputSpec) string {
	if spec == nil {
		return ""
	}
	var lines []string
	add := func(ex cmd.CommandExample) {
		if ex.Command == "" {
			return
		}
		if ex.Description != "" {
			lines = append(lines, "# "+ex.Description)
		}
		lines = append(lines, ex.Command)
	}
	for _, ex := range spec.ExampleList() {
		add(ex)
	}
	add(spec.JQ())
	return strings.Join(lines, "\n")
}

// renderOutputSection produces the Output section appended to --help when
// --help-output is set. Returns "" if there's nothing to append (e.g.
// non-leaf middle command, or root with no tree configured).
func renderOutputSection(path []string, tree cmd.Command, styles fang.Styles) string {
	// Root command: render the standard error table.
	if len(path) <= 1 {
		return renderRootOutput(styles)
	}

	def, ok := resolveCommand(tree, path[1:])
	if !ok {
		return ""
	}

	// Non-leaf middle: nothing appended.
	if len(def.Children) > 0 {
		return ""
	}

	var b strings.Builder
	if s := renderLeafErrors(def, styles); s != "" {
		b.WriteString(s)
	}
	if s := renderLeafText(def, styles); s != "" {
		b.WriteString(s)
	}
	if s := renderLeafJSON(def, styles); s != "" {
		b.WriteString(s)
	}
	return b.String()
}

func renderRootOutput(styles fang.Styles) string {
	var b strings.Builder
	b.WriteString(styles.Title.Render("exit codes"))
	b.WriteString("\n")
	for _, e := range cmd.StandardErrors() {
		b.WriteString(formatErrorLine(e))
	}
	return b.String()
}

func renderLeafErrors(def cmd.Command, styles fang.Styles) string {
	var b strings.Builder
	b.WriteString(styles.Title.Render("exit codes"))
	b.WriteString("\n")
	for _, e := range def.Errors {
		b.WriteString(formatErrorLine(e))
	}
	b.WriteString(lipgloss.NewStyle().PaddingLeft(longPad).Render(
		"See `baseten --help-output` for the standard exit codes."))
	b.WriteString("\n")
	return b.String()
}

func formatErrorLine(e cmd.ErrorDesc) string {
	line := fmt.Sprintf("%-3d  %s — %s", int(e.Code), e.Name, e.Meaning)
	return lipgloss.NewStyle().PaddingLeft(longPad).Render(line) + "\n"
}

func renderLeafText(def cmd.Command, styles fang.Styles) string {
	text := def.Output.Text()
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(styles.Title.Render("text output"))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().PaddingLeft(longPad).Width(termWidth()).Render(text))
	b.WriteString("\n")
	return b.String()
}

func renderLeafJSON(def cmd.Command, styles fang.Styles) string {
	var b strings.Builder
	b.WriteString(styles.Title.Render("json output"))
	b.WriteString("\n")
	if prose := def.Output.JSON(); prose != "" {
		b.WriteString(lipgloss.NewStyle().PaddingLeft(longPad).Width(termWidth()).Render(prose))
		b.WriteString("\n")
	}

	body := jsonSchemaBlock(def.Output)
	padding := styles.Codeblock.Base.GetHorizontalPadding()
	blockWidth := 0
	for _, line := range strings.Split(body, "\n") {
		blockWidth = max(blockWidth, lipgloss.Width(line))
	}
	blockWidth = min(termWidth()-padding, blockWidth+padding)
	blockStyle := styles.Codeblock.Base.Width(blockWidth)
	b.WriteString(blockStyle.Render(body))
	b.WriteString("\n")
	return b.String()
}

func jsonSchemaBlock(spec cmd.CommandOutputSpec) string {
	typ := spec.JSONOutputType()
	jsonAny := reflect.TypeOf((cmd.JSONAny)(nil))
	jsonUndefined := reflect.TypeFor[cmd.JSONUndefined]()
	switch typ {
	case jsonAny:
		return "any JSON object"
	case jsonUndefined:
		return "undefined (raw passthrough, not guaranteed JSON)"
	}

	schema := (&jsonschema.Reflector{
		Anonymous:                 true,
		DoNotReference:            true,
		AllowAdditionalProperties: true,
	}).ReflectFromType(typ)
	if schema == nil {
		return "(schema unavailable)"
	}
	raw, err := schema.MarshalJSON()
	if err != nil {
		return fmt.Sprintf("(schema error: %v)", err)
	}
	pretty, err := prettyJSONSchema(raw)
	if err != nil {
		return fmt.Sprintf("(schema render error: %v)", err)
	}
	if spec.JSONArrayStreamedBool() {
		return "Streamed: --output json wraps records in an array, --output jsonl emits one per line.\nRecord schema:\n" + pretty
	}
	return pretty
}

// prettyJSONSchema renders a JSON document with this rule: any object or
// array whose immediate children are all literals (no nested object/array)
// is emitted on a single line; containers with nested containers are pretty-
// printed with 2-space indents. Key order from the input is preserved.
func prettyJSONSchema(raw []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	n, err := parseJSONNode(dec)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	writeJSONNode(&b, n, 0)
	return b.String(), nil
}

type jsonNode struct {
	kind    byte // 'o' object, 'a' array, 'l' literal
	literal string
	keys    []string
	values  []jsonNode
}

func (n jsonNode) hasNestedContainer() bool {
	switch n.kind {
	case 'o', 'a':
		for _, v := range n.values {
			if v.kind == 'o' || v.kind == 'a' {
				return true
			}
		}
	}
	return false
}

func parseJSONNode(dec *json.Decoder) (jsonNode, error) {
	tok, err := dec.Token()
	if err != nil {
		return jsonNode{}, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			out := jsonNode{kind: 'o'}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return jsonNode{}, err
				}
				val, err := parseJSONNode(dec)
				if err != nil {
					return jsonNode{}, err
				}
				out.keys = append(out.keys, keyTok.(string))
				out.values = append(out.values, val)
			}
			if _, err := dec.Token(); err != nil {
				return jsonNode{}, err
			}
			return out, nil
		}
		if t == '[' {
			out := jsonNode{kind: 'a'}
			for dec.More() {
				val, err := parseJSONNode(dec)
				if err != nil {
					return jsonNode{}, err
				}
				out.values = append(out.values, val)
			}
			if _, err := dec.Token(); err != nil {
				return jsonNode{}, err
			}
			return out, nil
		}
	}
	enc, err := json.Marshal(tok)
	if err != nil {
		return jsonNode{}, err
	}
	return jsonNode{kind: 'l', literal: string(enc)}, nil
}

func writeJSONNode(b *strings.Builder, n jsonNode, depth int) {
	switch n.kind {
	case 'l':
		b.WriteString(n.literal)
	case 'o':
		if !n.hasNestedContainer() {
			b.WriteByte('{')
			for i, k := range n.keys {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(strconv.Quote(k))
				b.WriteString(": ")
				writeJSONNode(b, n.values[i], depth)
			}
			b.WriteByte('}')
			return
		}
		b.WriteString("{\n")
		for i, k := range n.keys {
			b.WriteString(strings.Repeat("  ", depth+1))
			b.WriteString(strconv.Quote(k))
			b.WriteString(": ")
			writeJSONNode(b, n.values[i], depth+1)
			if i < len(n.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteByte('}')
	case 'a':
		if !n.hasNestedContainer() {
			b.WriteByte('[')
			for i, v := range n.values {
				if i > 0 {
					b.WriteString(", ")
				}
				writeJSONNode(b, v, depth)
			}
			b.WriteByte(']')
			return
		}
		b.WriteString("[\n")
		for i, v := range n.values {
			b.WriteString(strings.Repeat("  ", depth+1))
			writeJSONNode(b, v, depth+1)
			if i < len(n.values)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteByte(']')
	}
}

// resolveCommand walks the tree by name segments. Returns the matched
// [cmd.Command] and true on success.
func resolveCommand(root cmd.Command, segments []string) (cmd.Command, bool) {
	cur := root
	for _, seg := range segments {
		found := false
		for _, child := range cur.Children {
			if child.Name == seg {
				cur = child
				found = true
				break
			}
		}
		if !found {
			return cmd.Command{}, false
		}
	}
	return cur, true
}
