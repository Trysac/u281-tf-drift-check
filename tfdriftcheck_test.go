package tfdriftcheck_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tfdriftcheck "github.com/Trysac/u281-tf-drift-check"
)

// repeatReader yields an endless stream of one byte, so tests can feed Parse
// more input than the size limit without allocating it all up front.
type repeatReader byte

func (r repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func newPlanJSON(t *testing.T, formatVersion string, resourceDrift []map[string]interface{}) string {
	t.Helper()
	plan := map[string]interface{}{
		"format_version": formatVersion,
		"resource_drift": resourceDrift,
	}
	return mustJSON(t, plan)
}

func resourceDrift(address, typ string, actions []string, before, after interface{}) map[string]interface{} {
	return map[string]interface{}{
		"address": address,
		"type":    typ,
		"change": map[string]interface{}{
			"actions": actions,
			"before":  before,
			"after":   after,
		},
	}
}

func TestParse(t *testing.T) {
	t.Run("valid plan", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("aws_instance.web", "aws_instance", []string{"update"},
				map[string]interface{}{"ami": "ami-1", "tags": map[string]interface{}{"Env": "prod"}},
				map[string]interface{}{"ami": "ami-1", "tags": map[string]interface{}{"Env": "dev"}},
			),
		})
		plan, err := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		if err != nil {
			t.Fatalf("Parse error: %v", err)
		}
		if plan.FormatVersion != "1.0" {
			t.Errorf("FormatVersion = %q, want 1.0", plan.FormatVersion)
		}
		if len(plan.ResourceDrift) != 1 {
			t.Fatalf("ResourceDrift length = %d, want 1", len(plan.ResourceDrift))
		}
		if plan.ResourceDrift[0].Address != "aws_instance.web" {
			t.Errorf("Address = %q", plan.ResourceDrift[0].Address)
		}
		if !plan.ResourceDrift[0].Change.Before.Present || !plan.ResourceDrift[0].Change.After.Present {
			t.Error("Before/After should be present")
		}
	})

	t.Run("too large", func(t *testing.T) {
		// Exactly one byte past the default limit of 52_428_800.
		_, err := tfdriftcheck.Parse(repeatReader('{'), 0)
		var e *tfdriftcheck.Error
		if !errors.As(err, &e) || e.Code != "input_too_large" {
			t.Fatalf("expected input_too_large, got %v", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := tfdriftcheck.Parse(strings.NewReader("{"), 0)
		var e *tfdriftcheck.Error
		if !errors.As(err, &e) || e.Code != "invalid_json" {
			t.Fatalf("expected invalid_json, got %v", err)
		}
	})

	t.Run("unsupported format", func(t *testing.T) {
		planJSON := newPlanJSON(t, "2.0", []map[string]interface{}{})
		_, err := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		var e *tfdriftcheck.Error
		if !errors.As(err, &e) || e.Code != "unsupported_format" {
			t.Fatalf("expected unsupported_format, got %v", err)
		}
	})

	t.Run("too deep", func(t *testing.T) {
		// Build nested value deeper than 64.
		deep := `{"format_version":"1.0","resource_drift":[{"address":"a","type":"t","change":{"actions":["update"],"before":`
		for i := 0; i < 70; i++ {
			deep += `{"x":`
		}
		deep += `null`
		for i := 0; i < 70; i++ {
			deep += `}`
		}
		deep += `,"after":null}}]}`
		_, err := tfdriftcheck.Parse(strings.NewReader(deep), 0)
		var e *tfdriftcheck.Error
		if !errors.As(err, &e) || e.Code != "too_deep" {
			t.Fatalf("expected too_deep, got %v", err)
		}
	})
}

func TestDetect(t *testing.T) {
	t.Run("modified with diff", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("aws_instance.web", "aws_instance", []string{"update"},
				map[string]interface{}{"ami": "ami-1", "tags": map[string]interface{}{"Env": "prod"}},
				map[string]interface{}{"ami": "ami-1", "tags": map[string]interface{}{"Env": "dev"}},
			),
		})
		plan, err := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		if err != nil {
			t.Fatal(err)
		}
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		wantDrifts := []tfdriftcheck.Drift{
			{Address: "aws_instance.web", Type: "aws_instance", Kind: "modified", Paths: []string{"tags.Env"}},
		}
		if !reflect.DeepEqual(report.Drifts, wantDrifts) {
			t.Errorf("Drifts = %#v, want %#v", report.Drifts, wantDrifts)
		}
		if report.Ignored != 0 {
			t.Errorf("Ignored = %d, want 0", report.Ignored)
		}
	})

	t.Run("deleted", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("aws_instance.web", "aws_instance", []string{"delete"},
				map[string]interface{}{"ami": "ami-1"}, nil,
			),
		})
		plan, err := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		if err != nil {
			t.Fatal(err)
		}
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		want := []tfdriftcheck.Drift{
			{Address: "aws_instance.web", Type: "aws_instance", Kind: "deleted", Paths: []string{}},
		}
		if !reflect.DeepEqual(report.Drifts, want) {
			t.Errorf("Drifts = %#v, want %#v", report.Drifts, want)
		}
	})

	t.Run("no drift", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{})
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 0 || report.Ignored != 0 {
			t.Errorf("expected no drifts/ignored, got %#v", report)
		}
	})

	t.Run("added key", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("res", "type", []string{"update"},
				map[string]interface{}{"a": 1},
				map[string]interface{}{"a": 1, "b": 2},
			),
		})
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 1 || len(report.Drifts[0].Paths) != 1 || report.Drifts[0].Paths[0] != "b" {
			t.Errorf("unexpected %#v", report)
		}
	})

	t.Run("array diff", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("res", "type", []string{"update"},
				map[string]interface{}{"x": []interface{}{1, 2}},
				map[string]interface{}{"x": []interface{}{1, 3}},
			),
		})
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 1 || len(report.Drifts[0].Paths) != 1 || report.Drifts[0].Paths[0] != "x[1]" {
			t.Errorf("unexpected %#v", report)
		}
	})

	t.Run("array length", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("res", "type", []string{"update"},
				map[string]interface{}{"x": []interface{}{1}},
				map[string]interface{}{"x": []interface{}{1, 2}},
			),
		})
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 1 || len(report.Drifts[0].Paths) != 1 || report.Drifts[0].Paths[0] != "x[1]" {
			t.Errorf("unexpected %#v", report)
		}
	})

	t.Run("key with dot", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("res", "type", []string{"update"},
				map[string]interface{}{"tags": map[string]interface{}{"a.b": "v1"}},
				map[string]interface{}{"tags": map[string]interface{}{"a.b": "v2"}},
			),
		})
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 1 || len(report.Drifts[0].Paths) != 1 || report.Drifts[0].Paths[0] != `tags["a.b"]` {
			t.Errorf("unexpected %#v", report)
		}
	})

	t.Run("number precision", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("res", "type", []string{"update"},
				map[string]interface{}{"n": json.Number("1")},
				map[string]interface{}{"n": json.Number("1.0")},
			),
		})
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 0 {
			t.Errorf("expected no drift, got %#v", report)
		}

		planJSON = newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("res", "type", []string{"update"},
				map[string]interface{}{"n": json.Number("9007199254740993")},
				map[string]interface{}{"n": json.Number("9007199254740992")},
			),
		})
		plan, _ = tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report = tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 1 || len(report.Drifts[0].Paths) != 1 || report.Drifts[0].Paths[0] != "n" {
			t.Errorf("unexpected %#v", report)
		}
	})

	t.Run("ignore rule", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("aws_instance.web", "aws_instance", []string{"update"},
				map[string]interface{}{"ami": "ami-1", "tags": map[string]interface{}{"Env": "prod"}},
				map[string]interface{}{"ami": "ami-1", "tags": map[string]interface{}{"Env": "dev"}},
			),
		})
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{Ignore: []string{"aws_instance.*:tags.Env"}})
		if len(report.Drifts) != 0 {
			t.Errorf("expected drift to be removed, got %#v", report.Drifts)
		}
		if report.Ignored != 1 {
			t.Errorf("Ignored = %d, want 1", report.Ignored)
		}
	})

	t.Run("null vs missing", func(t *testing.T) {
		// before has key with null, after missing key -> change
		planJSON := `{"format_version":"1.0","resource_drift":[{"address":"r","type":"t","change":{"actions":["update"],"before":{"a":null},"after":{}}}]}`
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 1 || len(report.Drifts[0].Paths) != 1 || report.Drifts[0].Paths[0] != "a" {
			t.Errorf("unexpected %#v", report)
		}
	})

	t.Run("empty object vs missing", func(t *testing.T) {
		planJSON := `{"format_version":"1.0","resource_drift":[{"address":"r","type":"t","change":{"actions":["update"],"before":{},"after":{"a":{}}}}]}`
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 1 || len(report.Drifts[0].Paths) != 1 || report.Drifts[0].Paths[0] != "a" {
			t.Errorf("unexpected %#v", report)
		}
	})

	t.Run("key with space", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("r", "t", []string{"update"},
				map[string]interface{}{"key with space": "v1"},
				map[string]interface{}{"key with space": "v2"},
			),
		})
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 1 || len(report.Drifts[0].Paths) != 1 || report.Drifts[0].Paths[0] != `["key with space"]` {
			t.Errorf("unexpected %#v", report)
		}
	})

	t.Run("unicode key", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("r", "t", []string{"update"},
				map[string]interface{}{"clé": "v1"},
				map[string]interface{}{"clé": "v2"},
			),
		})
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 1 || len(report.Drifts[0].Paths) != 1 || report.Drifts[0].Paths[0] != `["clé"]` {
			t.Errorf("unexpected %#v", report)
		}
	})

	t.Run("100 drifted resources sorted", func(t *testing.T) {
		var resources []map[string]interface{}
		for i := 99; i >= 0; i-- {
			addr := fmt.Sprintf("res-%d", i)
			resources = append(resources, resourceDrift(addr, "t", []string{"update"},
				map[string]interface{}{"a": "old"},
				map[string]interface{}{"a": "new"},
			))
		}
		planJSON := newPlanJSON(t, "1.0", resources)
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		if len(report.Drifts) != 100 {
			t.Fatalf("expected 100 drifts, got %d", len(report.Drifts))
		}
		// Check sorted by address
		for i := 1; i < len(report.Drifts); i++ {
			if report.Drifts[i-1].Address > report.Drifts[i].Address {
				t.Errorf("drifts not sorted: %s > %s", report.Drifts[i-1].Address, report.Drifts[i].Address)
			}
		}
	})
}

func TestRenderText(t *testing.T) {
	t.Run("modified", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("aws_instance.web", "aws_instance", []string{"update"},
				map[string]interface{}{"ami": "ami-1", "tags": map[string]interface{}{"Env": "prod", "Secret": "secret-value-123"}},
				map[string]interface{}{"ami": "ami-1", "tags": map[string]interface{}{"Env": "dev", "Secret": "secret-value-123"}},
			),
		})
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		var buf bytes.Buffer
		err := tfdriftcheck.Render(&buf, report, "text")
		if err != nil {
			t.Fatalf("Render error: %v", err)
		}
		got := buf.String()
		want := "aws_instance.web modified: tags.Env\n1 resource drifted\n"
		if got != want {
			t.Errorf("Render output = %q, want %q", got, want)
		}
		if strings.Contains(got, "secret-value-123") {
			t.Error("output contains secret value")
		}
	})

	t.Run("deleted", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("aws_instance.web", "aws_instance", []string{"delete"},
				map[string]interface{}{"ami": "ami-1"}, nil,
			),
		})
		plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
		report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
		var buf bytes.Buffer
		tfdriftcheck.Render(&buf, report, "text")
		got := buf.String()
		want := "aws_instance.web deleted\n1 resource drifted\n"
		if got != want {
			t.Errorf("Render output = %q, want %q", got, want)
		}
	})
}

func TestRenderJSON(t *testing.T) {
	planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
		resourceDrift("aws_instance.web", "aws_instance", []string{"update"},
			map[string]interface{}{"ami": "ami-1", "tags": map[string]interface{}{"Env": "prod"}},
			map[string]interface{}{"ami": "ami-1", "tags": map[string]interface{}{"Env": "dev"}},
		),
	})
	plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
	report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
	var buf bytes.Buffer
	tfdriftcheck.Render(&buf, report, "json")
	var decoded struct {
		Drifts []struct {
			Address string   `json:"address"`
			Type    string   `json:"type"`
			Kind    string   `json:"kind"`
			Paths   []string `json:"paths"`
		} `json:"drifts"`
		Ignored int `json:"ignored"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if len(decoded.Drifts) != 1 || decoded.Drifts[0].Paths[0] != "tags.Env" || decoded.Ignored != 0 {
		t.Errorf("unexpected JSON: %+v", decoded)
	}
}

func TestRun(t *testing.T) {
	t.Run("drift exit 1", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("aws_instance.web", "aws_instance", []string{"update"},
				map[string]interface{}{"a": "old"},
				map[string]interface{}{"a": "new"},
			),
		})
		planFile := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(planFile, []byte(planJSON), 0644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := tfdriftcheck.Run([]string{planFile}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("no drift exit 0", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{})
		planFile := filepath.Join(t.TempDir(), "plan.json")
		os.WriteFile(planFile, []byte(planJSON), 0644)
		var stdout, stderr bytes.Buffer
		code := tfdriftcheck.Run([]string{planFile}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("exit code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("missing file exit 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := tfdriftcheck.Run([]string{"/nonexistent"}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("exit code = %d, want 2; stderr=%q", code, stderr.String())
		}
	})

	t.Run("unknown flag exit 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := tfdriftcheck.Run([]string{"--bad", "file"}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("exit code = %d, want 2; stderr=%q", code, stderr.String())
		}
	})

	t.Run("repeatable ignore", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("aws_instance.web", "aws_instance", []string{"update"},
				map[string]interface{}{"a": "old", "b": "old"},
				map[string]interface{}{"a": "new", "b": "new"},
			),
		})
		planFile := filepath.Join(t.TempDir(), "plan.json")
		os.WriteFile(planFile, []byte(planJSON), 0644)
		var stdout, stderr bytes.Buffer
		code := tfdriftcheck.Run([]string{"--ignore", "aws_instance.*:a", "--ignore", "aws_instance.*:b", planFile}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
		}
	})
}

func TestDetectTypeChanges(t *testing.T) {
	planJSON := `{"format_version":"1.0","resource_drift":[{"address":"r","type":"t","change":{"actions":["update"],"before":{"s":"x","b":true,"arr":[1],"obj":{"x":1},"n":null},"after":{"s":1,"b":"true","arr":"x","obj":1,"n":"x"}}}]}`
	plan, err := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
	if err != nil {
		t.Fatal(err)
	}
	report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
	want := []string{"arr", "b", "n", "obj", "s"}
	if len(report.Drifts) != 1 || !reflect.DeepEqual(report.Drifts[0].Paths, want) {
		t.Errorf("unexpected %#v, want paths %v", report, want)
	}
}

func TestIgnoreQuotedKey(t *testing.T) {
	planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
		resourceDrift("r", "t", []string{"update"},
			map[string]interface{}{`k"q`: "v1"},
			map[string]interface{}{`k"q`: "v2"},
		),
	})
	plan, err := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
	if err != nil {
		t.Fatal(err)
	}
	report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{Ignore: []string{"r:*"}})
	if len(report.Drifts) != 0 {
		t.Errorf("expected drift to be removed, got %#v", report.Drifts)
	}
	if report.Ignored != 1 {
		t.Errorf("Ignored = %d, want 1", report.Ignored)
	}
}

func TestErrorString(t *testing.T) {
	err := &tfdriftcheck.Error{Code: "invalid_json", Msg: "bad input"}
	got := err.Error()
	if !strings.Contains(got, "invalid_json") || !strings.Contains(got, "bad input") {
		t.Errorf("Error() = %q", got)
	}
}

func TestRenderUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := tfdriftcheck.Render(&buf, tfdriftcheck.Report{}, "yaml"); err == nil {
		t.Error("expected an error for an unknown format")
	}
}

func TestRunUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"missing format value", []string{"--format"}},
		{"invalid format", []string{"--format", "yaml", "plan.json"}},
		{"invalid format equals", []string{"--format=yaml", "plan.json"}},
		{"missing ignore value", []string{"--ignore"}},
		{"no plan file", nil},
		{"multiple plan files", []string{"a.json", "b.json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := tfdriftcheck.Run(tc.args, &stdout, &stderr); code != 2 {
				t.Errorf("exit code = %d, want 2; stderr=%q", code, stderr.String())
			}
		})
	}

	t.Run("invalid ignore rule", func(t *testing.T) {
		planFile := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(planFile, []byte(`{"format_version":"1.0","resource_drift":[]}`), 0644); err != nil {
			t.Fatal(err)
		}
		for _, rule := range []string{"no-colon", "aws_instance.web.tags.Env", "aws_instance.*:tags["} {
			var stdout, stderr bytes.Buffer
			if code := tfdriftcheck.Run([]string{"--ignore", rule, planFile}, &stdout, &stderr); code != 2 {
				t.Errorf("rule %q: exit code = %d, want 2; stderr=%q", rule, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "invalid_rule") {
				t.Errorf("rule %q: stderr = %q, want invalid_rule", rule, stderr.String())
			}
		}
	})

	t.Run("parse error", func(t *testing.T) {
		planFile := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(planFile, []byte("{"), 0644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := tfdriftcheck.Run([]string{planFile}, &stdout, &stderr); code != 2 {
			t.Errorf("exit code = %d, want 2; stderr=%q", code, stderr.String())
		}
	})

	t.Run("render error", func(t *testing.T) {
		planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
			resourceDrift("aws_instance.web", "aws_instance", []string{"update"},
				map[string]interface{}{"a": "old"},
				map[string]interface{}{"a": "new"},
			),
		})
		planFile := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(planFile, []byte(planJSON), 0644); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		if code := tfdriftcheck.Run([]string{planFile}, failingWriter{}, &stderr); code != 2 {
			t.Errorf("exit code = %d, want 2; stderr=%q", code, stderr.String())
		}
	})
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestRunEqualsFlags(t *testing.T) {
	planJSON := newPlanJSON(t, "1.0", []map[string]interface{}{
		resourceDrift("aws_instance.web", "aws_instance", []string{"update"},
			map[string]interface{}{"a": "old", "b": "old"},
			map[string]interface{}{"a": "new", "b": "new"},
		),
	})
	planFile := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planFile, []byte(planJSON), 0644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := tfdriftcheck.Run([]string{"--format=json", "--ignore=aws_instance.*:a", planFile}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q", code, stderr.String())
	}
	var decoded struct {
		Drifts []struct {
			Paths []string `json:"paths"`
		} `json:"drifts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if len(decoded.Drifts) != 1 || !reflect.DeepEqual(decoded.Drifts[0].Paths, []string{"b"}) {
		t.Errorf("unexpected JSON: %+v", decoded)
	}
}

func Example() {
	planJSON := `{"format_version":"1.0","resource_drift":[{"address":"aws_instance.web","type":"aws_instance","change":{"actions":["update"],"before":{"ami":"ami-1","tags":{"Env":"prod"}},"after":{"ami":"ami-1","tags":{"Env":"dev"}}}}]}`
	plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
	report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{})
	var buf bytes.Buffer
	tfdriftcheck.Render(&buf, report, "text")
	fmt.Print(buf.String())
	// Output:
	// aws_instance.web modified: tags.Env
	// 1 resource drifted
}
