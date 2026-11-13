package schemadiff

import (
	"fmt"
	"strings"
	"testing"
)

func mustParse(t *testing.T, doc string) *Schema {
	t.Helper()
	s, err := Parse([]byte(doc), "")
	if err != nil {
		t.Fatalf("parse %s: %v", doc, err)
	}
	return s
}

// render turns changes into "severity path message" lines for compact assertions.
func render(changes []Change) string {
	lines := make([]string, len(changes))
	for i, c := range changes {
		lines[i] = fmt.Sprintf("%s %s %s", c.Severity, c.Path, c.Message)
	}
	return strings.Join(lines, "\n")
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name          string
		before, after string
		// Expected output per mode. An empty string means no changes.
		output, input, both string
	}{
		{
			name:   "identical",
			before: `{"type": "object", "properties": {"a": {"type": "string"}}}`,
			after:  `{"properties": {"a": {"type": "string"}}, "type": "object"}`,
		},
		{
			name:   "property removed",
			before: `{"properties": {"a": {}, "b": {}}}`,
			after:  `{"properties": {"a": {}}}`,
			output: "breaking $.b property removed",
			input:  "non-breaking $.b property removed",
			both:   "breaking $.b property removed",
		},
		{
			name:   "property removed from a closed object",
			before: `{"properties": {"a": {}, "b": {}}, "additionalProperties": false}`,
			after:  `{"properties": {"a": {}}, "additionalProperties": false}`,
			output: "breaking $.b property removed",
			input:  "breaking $.b property removed",
			both:   "breaking $.b property removed",
		},
		{
			name:   "optional property added",
			before: `{"properties": {}}`,
			after:  `{"properties": {"a": {}}}`,
			output: "non-breaking $.a optional property added",
			input:  "non-breaking $.a optional property added",
			both:   "non-breaking $.a optional property added",
		},
		{
			name:   "required property added",
			before: `{"properties": {}}`,
			after:  `{"properties": {"a": {}}, "required": ["a"]}`,
			output: "non-breaking $.a required property added",
			input:  "breaking $.a required property added",
			both:   "breaking $.a required property added",
		},
		{
			name:   "property becomes required",
			before: `{"properties": {"a": {}}}`,
			after:  `{"properties": {"a": {}}, "required": ["a"]}`,
			output: "non-breaking $.a now required",
			input:  "breaking $.a now required",
			both:   "breaking $.a now required",
		},
		{
			name:   "property becomes optional",
			before: `{"properties": {"a": {}}, "required": ["a"]}`,
			after:  `{"properties": {"a": {}}}`,
			output: "breaking $.a no longer required",
			input:  "non-breaking $.a no longer required",
			both:   "breaking $.a no longer required",
		},
		{
			name:   "type widened",
			before: `{"type": "string"}`,
			after:  `{"type": ["string", "null"]}`,
			output: "breaking $ can now be null",
			input:  "non-breaking $ can now be null",
			both:   "breaking $ can now be null",
		},
		{
			name:   "openapi nullable counts as null",
			before: `{"type": "string"}`,
			after:  `{"type": "string", "nullable": true}`,
			output: "breaking $ can now be null",
			input:  "non-breaking $ can now be null",
			both:   "breaking $ can now be null",
		},
		{
			name:   "type narrowed",
			before: `{"type": ["string", "integer"]}`,
			after:  `{"type": "string"}`,
			output: "non-breaking $ can no longer be integer",
			input:  "breaking $ can no longer be integer",
			both:   "breaking $ can no longer be integer",
		},
		{
			name:   "integer to number widens",
			before: `{"type": "integer"}`,
			after:  `{"type": "number"}`,
			output: "breaking $ can now be number",
			input:  "non-breaking $ can now be number",
			both:   "breaking $ can now be number",
		},
		{
			name:   "number to integer narrows",
			before: `{"type": "number"}`,
			after:  `{"type": "integer"}`,
			output: "non-breaking $ can no longer be number",
			input:  "breaking $ can no longer be number",
			both:   "breaking $ can no longer be number",
		},
		{
			name:   "any to typed",
			before: `{}`,
			after:  `{"type": "string"}`,
			output: "non-breaking $ type narrowed from any to string",
			input:  "breaking $ type narrowed from any to string",
			both:   "breaking $ type narrowed from any to string",
		},
		{
			name:   "enum value added",
			before: `{"enum": ["paid", "pending"]}`,
			after:  `{"enum": ["paid", "pending", "refunded"]}`,
			output: `breaking $ new allowed value "refunded"`,
			input:  `non-breaking $ new allowed value "refunded"`,
			both:   `breaking $ new allowed value "refunded"`,
		},
		{
			name:   "enum value removed",
			before: `{"enum": ["paid", "pending"]}`,
			after:  `{"enum": ["paid"]}`,
			output: `non-breaking $ value "pending" removed`,
			input:  `breaking $ value "pending" removed`,
			both:   `breaking $ value "pending" removed`,
		},
		{
			name:   "const is an enum of one",
			before: `{"const": "v1"}`,
			after:  `{"enum": ["v1"]}`,
			output: "",
			input:  "",
			both:   "",
		},
		{
			name:   "enum dropped",
			before: `{"enum": [1, 2]}`,
			after:  `{"type": "integer", "enum": null}`,
			output: "non-breaking $ type narrowed from any to integer\nbreaking $ no longer restricted to fixed values",
			input:  "breaking $ type narrowed from any to integer\nnon-breaking $ no longer restricted to fixed values",
			both:   "breaking $ type narrowed from any to integer\nbreaking $ no longer restricted to fixed values",
		},
		{
			name:   "max length tightened",
			before: `{"maxLength": 64}`,
			after:  `{"maxLength": 32}`,
			output: "non-breaking $ maxLength 64 -> 32 (tightened)",
			input:  "breaking $ maxLength 64 -> 32 (tightened)",
			both:   "breaking $ maxLength 64 -> 32 (tightened)",
		},
		{
			name:   "minimum loosened",
			before: `{"minimum": 1}`,
			after:  `{"minimum": 0}`,
			output: "review $ minimum 1 -> 0 (loosened)",
			input:  "non-breaking $ minimum 1 -> 0 (loosened)",
			both:   "review $ minimum 1 -> 0 (loosened)",
		},
		{
			name:   "bound added and removed",
			before: `{"maxItems": 10}`,
			after:  `{"minItems": 1}`,
			output: "non-breaking $ minItems none -> 1 (tightened)\nreview $ maxItems 10 -> none (loosened)",
			input:  "breaking $ minItems none -> 1 (tightened)\nnon-breaking $ maxItems 10 -> none (loosened)",
			both:   "breaking $ minItems none -> 1 (tightened)\nreview $ maxItems 10 -> none (loosened)",
		},
		{
			name:   "pattern changed",
			before: `{"pattern": "^[A-Z]+$"}`,
			after:  `{"pattern": "^[A-Z0-9]+$"}`,
			output: "review $ pattern changed: ^[A-Z]+$ -> ^[A-Z0-9]+$",
			input:  "breaking $ pattern changed: ^[A-Z]+$ -> ^[A-Z0-9]+$",
			both:   "breaking $ pattern changed: ^[A-Z]+$ -> ^[A-Z0-9]+$",
		},
		{
			name:   "format changed",
			before: `{"type": "string", "format": "date"}`,
			after:  `{"type": "string", "format": "date-time"}`,
			output: `review $ format "date" -> "date-time"`,
			input:  `review $ format "date" -> "date-time"`,
			both:   `review $ format "date" -> "date-time"`,
		},
		{
			name:   "object closed",
			before: `{"type": "object"}`,
			after:  `{"type": "object", "additionalProperties": false}`,
			output: "non-breaking $ unknown properties are now rejected",
			input:  "breaking $ unknown properties are now rejected",
			both:   "breaking $ unknown properties are now rejected",
		},
		{
			name:   "nested array items",
			before: `{"properties": {"lines": {"type": "array", "items": {"properties": {"sku": {"type": "string"}}}}}}`,
			after:  `{"properties": {"lines": {"type": "array", "items": {"properties": {"sku": {"type": "integer"}}}}}}`,
			output: "breaking $.lines[].sku can now be integer\nnon-breaking $.lines[].sku can no longer be string",
			input:  "non-breaking $.lines[].sku can now be integer\nbreaking $.lines[].sku can no longer be string",
			both:   "breaking $.lines[].sku can now be integer\nbreaking $.lines[].sku can no longer be string",
		},
		{
			name:   "combinators are flagged for review",
			before: `{"oneOf": [{"type": "string"}, {"type": "integer"}]}`,
			after:  `{"oneOf": [{"type": "string"}]}`,
			output: "review $ allOf, anyOf, oneOf, not or tuple items changed; not analysed",
			input:  "review $ allOf, anyOf, oneOf, not or tuple items changed; not analysed",
			both:   "review $ allOf, anyOf, oneOf, not or tuple items changed; not analysed",
		},
	}

	for _, tt := range tests {
		before, after := mustParse(t, tt.before), mustParse(t, tt.after)
		for _, m := range []struct {
			mode Mode
			want string
		}{{Output, tt.output}, {Input, tt.input}, {Both, tt.both}} {
			t.Run(tt.name+"/"+m.mode.String(), func(t *testing.T) {
				if got := render(Compare(before, after, m.mode)); got != m.want {
					t.Errorf("got:\n%s\nwant:\n%s", got, m.want)
				}
			})
		}
	}
}

func TestCompareResolvesRefs(t *testing.T) {
	before := mustParse(t, `{
		"$defs": {"Money": {"type": "object", "properties": {"amount": {"type": "integer"}}, "required": ["amount"]}},
		"properties": {"total": {"$ref": "#/$defs/Money"}, "tax": {"$ref": "#/$defs/Money"}}
	}`)
	after := mustParse(t, `{
		"$defs": {"Money": {"type": "object", "properties": {"amount": {"type": "integer"}}}},
		"properties": {"total": {"$ref": "#/$defs/Money"}, "tax": {"$ref": "#/$defs/Money"}}
	}`)
	want := "breaking $.tax.amount no longer required\nbreaking $.total.amount no longer required"
	if got := render(Compare(before, after, Output)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCompareTerminatesOnRecursiveSchemas(t *testing.T) {
	doc := func(childType string) string {
		return `{"$defs": {"Node": {"type": "object", "properties": {
			"name": {"type": "` + childType + `"},
			"children": {"type": "array", "items": {"$ref": "#/$defs/Node"}}
		}}}, "$ref": "#/$defs/Node"}`
	}
	got := render(Compare(mustParse(t, doc("string")), mustParse(t, doc("integer")), Input))
	want := "non-breaking $.name can now be integer\nbreaking $.name can no longer be string"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ doc, pointer, want string }{
		{`{`, "", "invalid JSON"},
		{`{}`, "/missing", `no key "missing"`},
		{`{}`, "nope", "must start with /"},
		{`{"$ref": "other.json#/a"}`, "", "only local $ref"},
		{`{"$ref": "#/nowhere"}`, "", `no key "nowhere"`},
		{`{"type": 5}`, "", "must be a string or an array"},
		{`{"properties": {"a": false}}`, "", "false schema"},
		{`{"required": [1]}`, "", "entries must be strings"},
		{`[1]`, "", "must be an object or a boolean"},
	}
	for _, tt := range tests {
		_, err := Parse([]byte(tt.doc), tt.pointer)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Parse(%s, %q) error = %v, want it to contain %q", tt.doc, tt.pointer, err, tt.want)
		}
	}
}

func TestParsePointerIntoOpenAPI(t *testing.T) {
	doc := `{"components": {"schemas": {
		"a/b": {"type": "string"},
		"Order": {"type": "object", "properties": {"id": {"$ref": "#/components/schemas/a~1b"}}}
	}}}`
	s, err := Parse([]byte(doc), "/components/schemas/Order")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Properties["id"].Types; len(got) != 1 || got[0] != "string" {
		t.Errorf("id types = %v, want [string]", got)
	}
}

func TestParseMode(t *testing.T) {
	for _, s := range []string{"output", "input", "both"} {
		m, err := ParseMode(s)
		if err != nil || m.String() != s {
			t.Errorf("ParseMode(%q) = %v, %v", s, m, err)
		}
	}
	if _, err := ParseMode("sideways"); err == nil {
		t.Error("expected an error for an unknown mode")
	}
}
