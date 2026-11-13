// Package schemadiff compares two versions of a JSON Schema and classifies
// each difference by whether it breaks producers or consumers of the data.
package schemadiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Schema is the subset of JSON Schema that schemadiff reasons about. Keywords
// it does not model are either ignored (annotations such as description) or
// captured as raw JSON in Unanalysed so a change to them is still reported.
type Schema struct {
	// Types is sorted and deduplicated. Empty means any type is allowed.
	Types      []string
	Properties map[string]*Schema
	Required   map[string]bool
	// AdditionalProperties is false only when the schema rejects properties
	// it doesn't list. A schema-valued additionalProperties counts as true.
	AdditionalProperties bool
	Items                *Schema
	// Enum holds the canonical JSON of each allowed value, sorted. Nil means
	// the values are unconstrained. A const is an enum of one.
	Enum    []string
	Format  string
	Pattern *string
	Bounds  map[string]float64
	// Unanalysed is the canonical JSON of allOf, anyOf, oneOf, not and tuple
	// items. Empty when none are present.
	Unanalysed string
}

var boundKeywords = []string{
	"minimum", "exclusiveMinimum", "minLength", "minItems", "minProperties",
	"maximum", "exclusiveMaximum", "maxLength", "maxItems", "maxProperties",
}

var unanalysedKeywords = []string{"allOf", "anyOf", "oneOf", "not"}

// LoadFile reads a JSON document and returns the schema at pointer, a JSON
// Pointer such as "/components/schemas/Order". An empty pointer selects the
// whole document.
func LoadFile(path, pointer string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := Parse(data, pointer)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Parse decodes a JSON document and returns the schema at pointer.
func Parse(data []byte, pointer string) (*Schema, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	node, err := resolvePointer(root, pointer)
	if err != nil {
		return nil, err
	}
	p := &parser{root: root, refs: map[string]*Schema{}}
	return p.build(node, "#"+pointer)
}

type parser struct {
	root any
	// refs maps each $ref already seen to its schema. The entry is created
	// before the target is built, so a recursive schema resolves to the same
	// pointer instead of recursing forever.
	refs map[string]*Schema
}

func (p *parser) build(node any, at string) (*Schema, error) {
	switch n := node.(type) {
	case bool:
		if n {
			return &Schema{AdditionalProperties: true}, nil
		}
		return nil, fmt.Errorf("%s: the false schema is not supported", at)
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok {
			return p.ref(ref, at)
		}
		return p.object(n, at)
	default:
		return nil, fmt.Errorf("%s: a schema must be an object or a boolean", at)
	}
}

func (p *parser) ref(ref, at string) (*Schema, error) {
	if s, ok := p.refs[ref]; ok {
		return s, nil
	}
	if !strings.HasPrefix(ref, "#") {
		return nil, fmt.Errorf("%s: only local $ref values are supported, got %q", at, ref)
	}
	s := &Schema{}
	p.refs[ref] = s
	target, err := resolvePointer(p.root, ref[1:])
	if err != nil {
		return nil, fmt.Errorf("%s: $ref %q: %w", at, ref, err)
	}
	built, err := p.build(target, ref)
	if err != nil {
		return nil, err
	}
	*s = *built
	return s, nil
}

func (p *parser) object(n map[string]any, at string) (*Schema, error) {
	s := &Schema{
		Properties:           map[string]*Schema{},
		Required:             map[string]bool{},
		AdditionalProperties: true,
		Bounds:               map[string]float64{},
	}

	types, err := parseTypes(n, at)
	if err != nil {
		return nil, err
	}
	s.Types = types

	if props, ok := n["properties"].(map[string]any); ok {
		for name, child := range props {
			built, err := p.build(child, at+"/properties/"+escapeToken(name))
			if err != nil {
				return nil, err
			}
			s.Properties[name] = built
		}
	}
	if req, ok := n["required"].([]any); ok {
		for _, r := range req {
			name, ok := r.(string)
			if !ok {
				return nil, fmt.Errorf("%s/required: entries must be strings", at)
			}
			s.Required[name] = true
		}
	}
	if ap, ok := n["additionalProperties"].(bool); ok {
		s.AdditionalProperties = ap
	}

	unanalysed := map[string]any{}
	switch items := n["items"].(type) {
	case map[string]any, bool:
		built, err := p.build(items, at+"/items")
		if err != nil {
			return nil, err
		}
		s.Items = built
	case []any:
		unanalysed["items"] = items
	}

	if values, ok := n["enum"].([]any); ok {
		s.Enum = canonicalSet(values)
	}
	if c, ok := n["const"]; ok {
		s.Enum = canonicalSet([]any{c})
	}
	if f, ok := n["format"].(string); ok {
		s.Format = f
	}
	if pat, ok := n["pattern"].(string); ok {
		s.Pattern = &pat
	}
	for _, key := range boundKeywords {
		num, ok := n[key].(json.Number)
		if !ok {
			continue
		}
		v, err := num.Float64()
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", at, key, err)
		}
		s.Bounds[key] = v
	}

	for _, key := range unanalysedKeywords {
		if v, ok := n[key]; ok {
			unanalysed[key] = v
		}
	}
	if len(unanalysed) > 0 {
		s.Unanalysed = canonical(unanalysed)
	}
	return s, nil
}

func parseTypes(n map[string]any, at string) ([]string, error) {
	var types []string
	switch t := n["type"].(type) {
	case nil:
	case string:
		types = []string{t}
	case []any:
		for _, v := range t {
			name, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("%s/type: entries must be strings", at)
			}
			types = append(types, name)
		}
	default:
		return nil, fmt.Errorf("%s/type: must be a string or an array of strings", at)
	}
	// OpenAPI 3.0 expresses null as a flag rather than a type.
	if nullable, _ := n["nullable"].(bool); nullable && len(types) > 0 {
		types = append(types, "null")
	}
	sort.Strings(types)
	return compactStrings(types), nil
}

func resolvePointer(root any, pointer string) (any, error) {
	if pointer == "" {
		return root, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("JSON pointer %q must start with /", pointer)
	}
	node := root
	for _, raw := range strings.Split(pointer[1:], "/") {
		token := strings.NewReplacer("~1", "/", "~0", "~").Replace(raw)
		switch n := node.(type) {
		case map[string]any:
			next, ok := n[token]
			if !ok {
				return nil, fmt.Errorf("JSON pointer %q: no key %q", pointer, token)
			}
			node = next
		case []any:
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(n) {
				return nil, fmt.Errorf("JSON pointer %q: bad index %q", pointer, token)
			}
			node = n[i]
		default:
			return nil, fmt.Errorf("JSON pointer %q: cannot descend into a scalar", pointer)
		}
	}
	return node, nil
}

func escapeToken(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}

// canonical returns JSON with object keys sorted, so equal values compare equal.
func canonical(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Values came from json.Decode, so they always re-encode.
		panic(err)
	}
	return string(b)
}

func canonicalSet(values []any) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, canonical(v))
	}
	sort.Strings(out)
	return compactStrings(out)
}

func compactStrings(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}
