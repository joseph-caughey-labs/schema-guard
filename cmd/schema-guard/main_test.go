package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	v1    = "../../examples/orders-v1.json"
	v2    = "../../examples/orders-v2.json"
	order = "/components/schemas/Order"
)

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestOutputModeFindsBreakingChanges(t *testing.T) {
	code, out, _ := runCLI("-mode", "output", "-pointer", order, v1, v2)
	if code != exitBreaking {
		t.Fatalf("exit = %d, want %d", code, exitBreaking)
	}
	want := `breaking      $.lines[].quantity  can now be number
breaking      $.note              can now be null
review        $.note              maxLength 200 -> 500 (loosened)
non-breaking  $.placedAt          optional property added
breaking      $.status            new allowed value "refunded"

3 breaking, 1 review, 1 non-breaking (mode: output)
`
	if out != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
}

func TestInputModeAcceptsWidening(t *testing.T) {
	code, out, _ := runCLI("-mode", "input", "-pointer", order, v1, v2)
	if code != exitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, exitOK, out)
	}
	if !strings.Contains(out, "0 breaking, 0 review, 5 non-breaking (mode: input)") {
		t.Errorf("unexpected summary:\n%s", out)
	}
}

func TestReversedVersionsBreakInput(t *testing.T) {
	code, out, _ := runCLI("-mode", "input", "-pointer", order, v2, v1)
	if code != exitBreaking {
		t.Fatalf("exit = %d, want %d", code, exitBreaking)
	}
	if !strings.Contains(out, `breaking      $.status            value "refunded" removed`) {
		t.Errorf("missing removed enum value:\n%s", out)
	}
}

func TestStrictFailsOnReview(t *testing.T) {
	dir := t.TempDir()
	before := writeFile(t, dir, "old.json", `{"maxLength": 10}`)
	after := writeFile(t, dir, "new.json", `{"maxLength": 20}`)

	if code, _, _ := runCLI("-mode", "output", before, after); code != exitOK {
		t.Errorf("without -strict: exit = %d, want %d", code, exitOK)
	}
	if code, _, _ := runCLI("-mode", "output", "-strict", before, after); code != exitBreaking {
		t.Errorf("with -strict: exit = %d, want %d", code, exitBreaking)
	}
}

func TestNoChanges(t *testing.T) {
	code, out, _ := runCLI("-pointer", order, v1, v1)
	if code != exitOK || out != "no changes (mode: both)\n" {
		t.Errorf("exit = %d, output = %q", code, out)
	}
}

func TestJSONFormat(t *testing.T) {
	code, out, _ := runCLI("-format", "json", "-pointer", order, v1, v2)
	if code != exitBreaking {
		t.Fatalf("exit = %d, want %d", code, exitBreaking)
	}
	var report struct {
		Mode     string `json:"mode"`
		Breaking int    `json:"breaking"`
		Changes  []struct {
			Path     string `json:"path"`
			Severity string `json:"severity"`
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if report.Mode != "both" || report.Breaking != 3 || len(report.Changes) != 5 {
		t.Errorf("unexpected report: %+v", report)
	}
	if report.Changes[0].Severity != "breaking" {
		t.Errorf("severity should serialise as text, got %q", report.Changes[0].Severity)
	}
}

func TestJSONFormatWithNoChangesHasEmptyList(t *testing.T) {
	_, out, _ := runCLI("-format", "json", "-pointer", order, v1, v1)
	if !strings.Contains(out, `"changes": []`) {
		t.Errorf("want an empty changes array, got:\n%s", out)
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{v1}, "usage: schema-guard"},
		{[]string{"-mode", "sideways", v1, v2}, "unknown mode"},
		{[]string{"-format", "yaml", v1, v2}, "unknown format"},
		{[]string{"missing.json", v2}, "missing.json"},
		{[]string{"-pointer", "/nope", v1, v2}, `no key "nope"`},
		{[]string{"-bogus", v1, v2}, "flag provided but not defined"},
	}
	for _, tt := range tests {
		code, _, errOut := runCLI(tt.args...)
		if code != exitUsage || !strings.Contains(errOut, tt.want) {
			t.Errorf("args %v: exit = %d, stderr = %q, want %q", tt.args, code, errOut, tt.want)
		}
	}
}

func TestHelpExitsZero(t *testing.T) {
	if code, _, errOut := runCLI("-h"); code != exitOK || !strings.Contains(errOut, "-mode") {
		t.Errorf("exit = %d, stderr = %q", code, errOut)
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
