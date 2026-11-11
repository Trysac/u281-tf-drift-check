package tfdriftcheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Error represents a typed error with a stable machine-readable code.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("tfdriftcheck: %s: %s", e.Code, e.Msg)
}

// truncate limits a string to max characters, used for error messages.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// Value represents an optional JSON value with presence information.
// It distinguishes between an absent key and an explicit null.
type Value struct {
	Present bool
	Data    interface{}
}

// UnmarshalJSON captures the raw JSON, parses it with depth limit,
// and stores the result. It implements json.Unmarshaler.
func (v *Value) UnmarshalJSON(data []byte) error {
	v.Present = true
	parsed, err := parseJSON(data, 64)
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			return err
		}
		return &Error{Code: "invalid_json", Msg: truncate(err.Error(), 200)}
	}
	v.Data = parsed
	return nil
}

// Plan represents the parsed Terraform plan JSON.
type Plan struct {
	FormatVersion string           `json:"format_version"`
	ResourceDrift []ResourceChange `json:"resource_drift"`
}

// ResourceChange represents a single drifted resource.
type ResourceChange struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	Change  Change `json:"change"`
}

// Change holds the actions and the before/after values.
type Change struct {
	Actions []string `json:"actions"`
	Before  Value    `json:"before"`
	After   Value    `json:"after"`
}

// Parse reads Terraform plan JSON from r, enforcing a maximum byte size.
// If maxBytes <= 0, a default of 52_428_800 is used. It returns a typed
// error for input too large, invalid JSON, unsupported format version,
// or values nested deeper than the limit.
func Parse(r io.Reader, maxBytes int64) (*Plan, error) {
	if maxBytes <= 0 {
		maxBytes = 52_428_800
	}
	limited := io.LimitReader(r, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, &Error{Code: "input_too_large", Msg: "input exceeds maximum size"}
	}
	var plan Plan
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&plan); err != nil {
		var e *Error
		if errors.As(err, &e) {
			return nil, e
		}
		return nil, &Error{Code: "invalid_json", Msg: truncate(err.Error(), 200)}
	}
	// Check format_version major is 1
	if parts := strings.Split(plan.FormatVersion, "."); len(parts) == 0 || parts[0] != "1" {
		return nil, &Error{Code: "unsupported_format", Msg: "format_version major is not 1"}
	}
	return &plan, nil
}

// Options configures drift detection.
type Options struct {
	Ignore []string // list of "<address glob>:<path glob>" rules
}

// Drift represents a single detected drift for a resource.
type Drift struct {
	Address string   `json:"address"`
	Type    string   `json:"type"`
	Kind    string   `json:"kind"` // "deleted" or "modified"
	Paths   []string `json:"paths"`
}

// Report contains the detection results.
type Report struct {
	Drifts  []Drift
	Ignored int
}

// Detect analyzes a parsed Plan and returns a Report.
// It applies ignore rules and sorts drifts by address.
func Detect(p *Plan, opts Options) Report {
	report := Report{Drifts: []Drift{}, Ignored: 0}
	for _, rc := range p.ResourceDrift {
		kind := "modified"
		// Determine kind: deleted if actions exactly ["delete"] or after is explicit null.
		if (len(rc.Change.Actions) == 1 && rc.Change.Actions[0] == "delete") ||
			(rc.Change.After.Present && rc.Change.After.Data == nil) {
			kind = "deleted"
		}
		var paths []string
		if kind == "modified" {
			// Compute diff between before and after.
			before := rc.Change.Before.Data
			after := rc.Change.After.Data
			if !rc.Change.Before.Present {
				before = nil
			}
			if !rc.Change.After.Present {
				after = nil
			}
			paths = diffValues(before, after, "")
			originalLen := len(paths)
			// Apply ignore rules.
			for _, rule := range opts.Ignore {
				if err := validateRule(rule); err != nil {
					continue // skip malformed rule
				}
				idx := strings.IndexByte(rule, ':')
				addrPattern := rule[:idx]
				pathPattern := rule[idx+1:]
				// Check if address matches.
				matched, err := path.Match(addrPattern, rc.Address)
				if err != nil || !matched {
					continue
				}
				// Filter paths that match the path pattern.
				filtered := paths[:0]
				for _, p := range paths {
					if !matchPath(pathPattern, p) {
						filtered = append(filtered, p)
					}
				}
				paths = filtered
			}
			if len(paths) == 0 {
				if originalLen > 0 {
					report.Ignored++
				}
				continue // skip this drift entirely
			}
			sort.Strings(paths)
			drift := Drift{Address: rc.Address, Type: rc.Type, Kind: kind, Paths: paths}
			report.Drifts = append(report.Drifts, drift)
		} else { // deleted
			drift := Drift{Address: rc.Address, Type: rc.Type, Kind: kind, Paths: []string{}}
			report.Drifts = append(report.Drifts, drift)
		}
	}
	// Sort drifts by address.
	sort.Slice(report.Drifts, func(i, j int) bool {
		return report.Drifts[i].Address < report.Drifts[j].Address
	})
	return report
}

// Render writes the report in the requested format ("text" or "json").
func Render(w io.Writer, r Report, format string) error {
	switch format {
	case "text":
		var b strings.Builder
		for _, d := range r.Drifts {
			if d.Kind == "deleted" {
				fmt.Fprintf(&b, "%s deleted\n", d.Address)
			} else {
				fmt.Fprintf(&b, "%s %s: %s\n", d.Address, d.Kind, strings.Join(d.Paths, ", "))
			}
		}
		if len(r.Drifts) == 1 {
			b.WriteString("1 resource drifted\n")
		} else {
			fmt.Fprintf(&b, "%d resources drifted\n", len(r.Drifts))
		}
		if _, err := io.WriteString(w, b.String()); err != nil {
			return err
		}
	case "json":
		type jsonDrift struct {
			Address string   `json:"address"`
			Type    string   `json:"type"`
			Kind    string   `json:"kind"`
			Paths   []string `json:"paths"`
		}
		type jsonReport struct {
			Drifts  []jsonDrift `json:"drifts"`
			Ignored int         `json:"ignored"`
		}
		jr := jsonReport{Drifts: []jsonDrift{}, Ignored: r.Ignored}
		for _, d := range r.Drifts {
			jd := jsonDrift{Address: d.Address, Type: d.Type, Kind: d.Kind, Paths: d.Paths}
			jr.Drifts = append(jr.Drifts, jd)
		}
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(jr); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown format: %s", format)
	}
	return nil
}

// Run is the CLI entry point. It parses arguments, reads the plan file,
// runs detection, renders the result, and returns an exit code:
// 0 for no drift, 1 for drift found, 2 for usage/IO/parse errors.
func Run(args []string, stdout, stderr io.Writer) int {
	var format string = "text"
	var ignores []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--format":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "missing value for --format")
				return 2
			}
			format = args[i+1]
			if format != "text" && format != "json" {
				fmt.Fprintln(stderr, "invalid format; must be text or json")
				return 2
			}
			i++
		case strings.HasPrefix(arg, "--format="):
			format = strings.TrimPrefix(arg, "--format=")
			if format != "text" && format != "json" {
				fmt.Fprintln(stderr, "invalid format; must be text or json")
				return 2
			}
		case arg == "--ignore":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "missing value for --ignore")
				return 2
			}
			ignores = append(ignores, args[i+1])
			i++
		case strings.HasPrefix(arg, "--ignore="):
			ignores = append(ignores, strings.TrimPrefix(arg, "--ignore="))
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "unknown flag: %s\n", arg)
			return 2
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "exactly one plan file is required")
		return 2
	}
	for _, rule := range ignores {
		if err := validateRule(rule); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 2
		}
	}
	planPath := positional[0]
	f, err := os.Open(planPath)
	if err != nil {
		fmt.Fprintf(stderr, "open plan: %v\n", err)
		return 2
	}
	defer f.Close()
	plan, err := Parse(f, 0)
	if err != nil {
		fmt.Fprintf(stderr, "parse: %v\n", err)
		return 2
	}
	report := Detect(plan, Options{Ignore: ignores})
	if err := Render(stdout, report, format); err != nil {
		fmt.Fprintf(stderr, "render: %v\n", err)
		return 2
	}
	if len(report.Drifts) > 0 {
		return 1
	}
	return 0
}

// parseJSON decodes the given bytes into an interface{} using a depth limit.
// It uses json.Decoder with UseNumber to preserve number precision.
func parseJSON(data []byte, maxDepth int) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return parseValue(dec, 0, maxDepth)
}

// parseValue recursively reads the next JSON value from dec, enforcing depth limit.
func parseValue(dec *json.Decoder, depth int, maxDepth int) (interface{}, error) {
	if depth > maxDepth {
		return nil, &Error{Code: "too_deep", Msg: "value nested deeper than 64 levels"}
	}
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if token == nil {
		return nil, nil // JSON null
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := make(map[string]interface{})
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("expected object key string")
				}
				value, err := parseValue(dec, depth+1, maxDepth)
				if err != nil {
					return nil, err
				}
				m[key] = value
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return m, nil
		case '[':
			var arr []interface{}
			for dec.More() {
				value, err := parseValue(dec, depth+1, maxDepth)
				if err != nil {
					return nil, err
				}
				arr = append(arr, value)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		default:
			return nil, fmt.Errorf("unexpected delimiter %v", t)
		}
	case string:
		return t, nil
	case bool:
		return t, nil
	case json.Number:
		return t, nil
	default:
		return nil, fmt.Errorf("unexpected token type %T", token)
	}
}

// diffValues compares two JSON values recursively and returns a list of paths
// where they differ. Paths are at the deepest differing node.
func diffValues(before, after interface{}, prefix string) []string {
	if before == nil || after == nil {
		if before == nil && after == nil {
			return nil
		}
		return []string{prefix}
	}
	switch b := before.(type) {
	case map[string]interface{}:
		a, ok := after.(map[string]interface{})
		if !ok {
			return []string{prefix}
		}
		return diffObjects(b, a, prefix)
	case []interface{}:
		a, ok := after.([]interface{})
		if !ok {
			return []string{prefix}
		}
		return diffArrays(b, a, prefix)
	case string:
		a, ok := after.(string)
		if !ok || b != a {
			return []string{prefix}
		}
		return nil
	case bool:
		a, ok := after.(bool)
		if !ok || b != a {
			return []string{prefix}
		}
		return nil
	case json.Number:
		a, ok := after.(json.Number)
		if !ok || !numbersEqual(b, a) {
			return []string{prefix}
		}
		return nil
	default:
		return []string{prefix}
	}
}

// diffObjects compares two maps by sorted key union.
func diffObjects(b, a map[string]interface{}, prefix string) []string {
	keys := make(map[string]bool)
	for k := range b {
		keys[k] = true
	}
	for k := range a {
		keys[k] = true
	}
	var sortedKeys []string
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)
	var paths []string
	for _, k := range sortedKeys {
		bv, bok := b[k]
		av, aok := a[k]
		if !bok || !aok {
			// one missing
			paths = append(paths, joinPath(prefix, k))
			continue
		}
		childPrefix := joinPath(prefix, k)
		childPaths := diffValues(bv, av, childPrefix)
		paths = append(paths, childPaths...)
	}
	return paths
}

// diffArrays compares two arrays by index up to the longer length.
func diffArrays(b, a []interface{}, prefix string) []string {
	maxLen := len(b)
	if len(a) > maxLen {
		maxLen = len(a)
	}
	var paths []string
	for i := 0; i < maxLen; i++ {
		if i >= len(b) || i >= len(a) {
			paths = append(paths, prefix+"["+strconv.Itoa(i)+"]")
			continue
		}
		childPrefix := prefix + "[" + strconv.Itoa(i) + "]"
		childPaths := diffValues(b[i], a[i], childPrefix)
		paths = append(paths, childPaths...)
	}
	return paths
}

// joinPath appends a key to a parent path following the grammar.
func joinPath(parent, key string) string {
	if isSimpleKey(key) {
		if parent == "" {
			return key
		}
		return parent + "." + key
	}
	if parent == "" {
		return "[" + strconv.Quote(key) + "]"
	}
	return parent + "[" + strconv.Quote(key) + "]"
}

// isSimpleKey reports whether key matches ^[A-Za-z_][A-Za-z0-9_-]*$.
func isSimpleKey(key string) bool {
	if len(key) == 0 {
		return false
	}
	first := key[0]
	if !((first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z') || first == '_') {
		return false
	}
	for i := 1; i < len(key); i++ {
		c := key[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// numbersEqual compares two json.Number values as exact decimal values.
func numbersEqual(a, b json.Number) bool {
	ra, ok1 := new(big.Rat).SetString(string(a))
	rb, ok2 := new(big.Rat).SetString(string(b))
	if !ok1 || !ok2 {
		return false
	}
	return ra.Cmp(rb) == 0
}

// validateRule checks an ignore rule of the form "<address glob>:<path glob>".
// Both sides must be non-empty and valid path.Match patterns.
func validateRule(rule string) error {
	idx := strings.IndexByte(rule, ':')
	if idx <= 0 || idx == len(rule)-1 {
		return &Error{
			Code: "invalid_rule",
			Msg:  truncate(fmt.Sprintf("ignore rule %q: want <address glob>:<path glob>", rule), 200),
		}
	}
	addrPattern, pathPattern := rule[:idx], rule[idx+1:]
	if _, err := path.Match(addrPattern, ""); err != nil {
		return &Error{
			Code: "invalid_rule",
			Msg:  truncate(fmt.Sprintf("ignore rule %q: bad address glob: %v", rule, err), 200),
		}
	}
	if _, err := path.Match(pathPattern, ""); err != nil {
		return &Error{
			Code: "invalid_rule",
			Msg:  truncate(fmt.Sprintf("ignore rule %q: bad path glob: %v", rule, err), 200),
		}
	}
	return nil
}

// matchPath checks if a path string matches a path glob pattern.
// Patterns use path.Match semantics but treat '.' as a separator.
func matchPath(pattern, pathStr string) bool {
	patternParts := strings.Split(pattern, ".")
	pathParts := splitPath(pathStr)
	if len(patternParts) != len(pathParts) {
		return false
	}
	for i := range patternParts {
		matched, err := path.Match(patternParts[i], pathParts[i])
		if err != nil || !matched {
			return false
		}
	}
	return true
}

// splitPath splits a path string into segments, respecting quoted keys.
// It splits on '.' only when not inside double quotes (Go %q).
func splitPath(s string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	escape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote {
			if escape {
				current.WriteByte(c)
				escape = false
			} else if c == '\\' {
				current.WriteByte(c)
				escape = true
			} else if c == '"' {
				current.WriteByte(c)
				inQuote = false
			} else {
				current.WriteByte(c)
			}
		} else {
			if c == '.' {
				parts = append(parts, current.String())
				current.Reset()
			} else if c == '"' {
				current.WriteByte(c)
				inQuote = true
			} else {
				current.WriteByte(c)
			}
		}
	}
	parts = append(parts, current.String())
	return parts
}
