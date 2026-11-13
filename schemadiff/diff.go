package schemadiff

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Mode says which side of the data flow to protect.
type Mode int

const (
	// Output is for data you produce and others read, such as API responses.
	// A change breaks if an existing reader could receive something it
	// doesn't expect.
	Output Mode = iota + 1
	// Input is for data others send you, such as request bodies. A change
	// breaks if something an existing sender produces would now be rejected.
	Input
	// Both is for data that flows in both directions, or when you don't know.
	Both
)

func ParseMode(s string) (Mode, error) {
	switch s {
	case "output":
		return Output, nil
	case "input":
		return Input, nil
	case "both":
		return Both, nil
	}
	return 0, fmt.Errorf("unknown mode %q: expected output, input or both", s)
}

func (m Mode) String() string {
	switch m {
	case Output:
		return "output"
	case Input:
		return "input"
	case Both:
		return "both"
	}
	return "unknown"
}

type Severity int

const (
	NonBreaking Severity = iota
	// Review marks a change schemadiff can't classify with confidence, such
	// as a changed pattern or an edit inside oneOf.
	Review
	Breaking
)

func (s Severity) String() string {
	switch s {
	case NonBreaking:
		return "non-breaking"
	case Review:
		return "review"
	case Breaking:
		return "breaking"
	}
	return "unknown"
}

func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

type Change struct {
	// Path locates the change, for example "$.items[].sku".
	Path     string   `json:"path"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// Compare lists every difference between old and new that affects which
// documents are valid, in a stable order.
func Compare(old, new *Schema, mode Mode) []Change {
	c := &comparer{mode: mode, active: map[[2]*Schema]bool{}}
	c.compare("$", old, new)
	return c.changes
}

type comparer struct {
	mode    Mode
	changes []Change
	// active holds the schema pairs on the current path. A pair that is
	// already active means a recursive schema has looped back on itself.
	// A pair reached by a different path (a shared $ref) is compared again,
	// so its changes are reported at every place it is used.
	active map[[2]*Schema]bool
}

// pick chooses the severity for the current mode. Both takes the worse of
// the two, since the change has to be safe for readers and senders.
func (c *comparer) pick(output, input Severity) Severity {
	switch c.mode {
	case Output:
		return output
	case Input:
		return input
	}
	return max(output, input)
}

func (c *comparer) add(path string, sev Severity, format string, args ...any) {
	c.changes = append(c.changes, Change{Path: path, Severity: sev, Message: fmt.Sprintf(format, args...)})
}

func (c *comparer) compare(path string, a, b *Schema) {
	key := [2]*Schema{a, b}
	if c.active[key] {
		return
	}
	c.active[key] = true
	defer delete(c.active, key)

	c.types(path, a, b)
	c.enum(path, a, b)
	c.bounds(path, a, b)
	c.strings(path, a, b)
	c.object(path, a, b)

	switch {
	case a.Items != nil && b.Items != nil:
		c.compare(path+"[]", a.Items, b.Items)
	case a.Items == nil && b.Items != nil:
		c.add(path+"[]", c.pick(NonBreaking, Breaking), "array items are now constrained")
	case a.Items != nil && b.Items == nil:
		c.add(path+"[]", c.pick(Breaking, NonBreaking), "array items are no longer constrained")
	}

	if a.Unanalysed != b.Unanalysed {
		c.add(path, Review, "allOf, anyOf, oneOf, not or tuple items changed; not analysed")
	}
}

// accepts reports whether a value of type t is valid under types. An integer
// is also a number.
func accepts(types []string, t string) bool {
	for _, have := range types {
		if have == t || (t == "integer" && have == "number") {
			return true
		}
	}
	return false
}

func (c *comparer) types(path string, a, b *Schema) {
	switch {
	case len(a.Types) == 0 && len(b.Types) == 0:
		return
	case len(a.Types) == 0:
		c.add(path, c.pick(NonBreaking, Breaking), "type narrowed from any to %s", strings.Join(b.Types, "|"))
		return
	case len(b.Types) == 0:
		c.add(path, c.pick(Breaking, NonBreaking), "type widened from %s to any", strings.Join(a.Types, "|"))
		return
	}
	for _, t := range b.Types {
		if !accepts(a.Types, t) {
			c.add(path, c.pick(Breaking, NonBreaking), "can now be %s", t)
		}
	}
	for _, t := range a.Types {
		if !accepts(b.Types, t) {
			c.add(path, c.pick(NonBreaking, Breaking), "can no longer be %s", t)
		}
	}
}

func (c *comparer) enum(path string, a, b *Schema) {
	switch {
	case a.Enum == nil && b.Enum == nil:
		return
	case a.Enum == nil:
		c.add(path, c.pick(NonBreaking, Breaking), "now restricted to %d allowed value(s)", len(b.Enum))
		return
	case b.Enum == nil:
		c.add(path, c.pick(Breaking, NonBreaking), "no longer restricted to fixed values")
		return
	}
	for _, v := range difference(b.Enum, a.Enum) {
		c.add(path, c.pick(Breaking, NonBreaking), "new allowed value %s", v)
	}
	for _, v := range difference(a.Enum, b.Enum) {
		c.add(path, c.pick(NonBreaking, Breaking), "value %s removed", v)
	}
}

func (c *comparer) bounds(path string, a, b *Schema) {
	for _, key := range boundKeywords {
		oldV, hadOld := a.Bounds[key]
		newV, hasNew := b.Bounds[key]
		if hadOld == hasNew && oldV == newV {
			continue
		}
		lower := strings.HasPrefix(key, "min") || key == "exclusiveMinimum"
		var tightened bool
		switch {
		case !hadOld:
			tightened = true
		case !hasNew:
			tightened = false
		case lower:
			tightened = newV > oldV
		default:
			tightened = newV < oldV
		}
		desc := fmt.Sprintf("%s %s -> %s", key, formatBound(oldV, hadOld), formatBound(newV, hasNew))
		if tightened {
			c.add(path, c.pick(NonBreaking, Breaking), "%s (tightened)", desc)
		} else {
			// Readers rarely rely on a bound, but some do (fixed-width
			// columns, UI layouts), so loosening one on output is flagged
			// for a person to check rather than called safe.
			c.add(path, c.pick(Review, NonBreaking), "%s (loosened)", desc)
		}
	}
}

func formatBound(v float64, ok bool) string {
	if !ok {
		return "none"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (c *comparer) strings(path string, a, b *Schema) {
	if a.Format != b.Format {
		c.add(path, Review, "format %q -> %q", a.Format, b.Format)
	}
	switch {
	case a.Pattern == nil && b.Pattern == nil:
	case a.Pattern == nil:
		c.add(path, c.pick(NonBreaking, Breaking), "pattern added: %s", *b.Pattern)
	case b.Pattern == nil:
		c.add(path, c.pick(Review, NonBreaking), "pattern removed: %s", *a.Pattern)
	case *a.Pattern != *b.Pattern:
		// Two regexes can't in general be compared for inclusion.
		c.add(path, c.pick(Review, Breaking), "pattern changed: %s -> %s", *a.Pattern, *b.Pattern)
	}
}

func (c *comparer) object(path string, a, b *Schema) {
	names := make([]string, 0, len(a.Properties)+len(b.Properties))
	for name := range a.Properties {
		names = append(names, name)
	}
	for name := range b.Properties {
		if _, ok := a.Properties[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		child := path + "." + name
		pa, pb := a.Properties[name], b.Properties[name]
		switch {
		case pb == nil:
			// A sender may still include the removed property. That only
			// fails if the new schema rejects unknown properties.
			input := NonBreaking
			if !b.AdditionalProperties {
				input = Breaking
			}
			c.add(child, c.pick(Breaking, input), "property removed")
		case pa == nil:
			if b.Required[name] {
				c.add(child, c.pick(NonBreaking, Breaking), "required property added")
			} else {
				c.add(child, NonBreaking, "optional property added")
			}
		default:
			switch {
			case !a.Required[name] && b.Required[name]:
				c.add(child, c.pick(NonBreaking, Breaking), "now required")
			case a.Required[name] && !b.Required[name]:
				c.add(child, c.pick(Breaking, NonBreaking), "no longer required")
			}
			c.compare(child, pa, pb)
		}
	}

	switch {
	case a.AdditionalProperties && !b.AdditionalProperties:
		c.add(path, c.pick(NonBreaking, Breaking), "unknown properties are now rejected")
	case !a.AdditionalProperties && b.AdditionalProperties:
		c.add(path, c.pick(Review, NonBreaking), "unknown properties are now allowed")
	}
}

// difference returns the members of a that are not in b. Both are sorted.
func difference(a, b []string) []string {
	var out []string
	for _, v := range a {
		i := sort.SearchStrings(b, v)
		if i == len(b) || b[i] != v {
			out = append(out, v)
		}
	}
	return out
}
