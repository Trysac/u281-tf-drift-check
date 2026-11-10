# tf-drift-check

![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)
[![Go Reference](https://pkg.go.dev/badge/github.com/Trysac/u281-tf-drift-check.svg)](https://pkg.go.dev/github.com/Trysac/u281-tf-drift-check)
[![CI](https://github.com/Trysac/u281-tf-drift-check/actions/workflows/ci.yml/badge.svg)](https://github.com/Trysac/u281-tf-drift-check/actions/workflows/ci.yml)

Infrastructure changed by hand in a console silently diverges from Terraform state, and the
drift only shows up mixed into the next apply. `terraform plan -refresh-only` reports it, but
as long JSON with full before and after values that may be sensitive, so teams neither read
nor share it. This library extracts which attribute paths drifted per resource, applies
ignore rules for noisy attributes and prints paths only, never values.

## Usage

```go
plan, _ := tfdriftcheck.Parse(strings.NewReader(planJSON), 0)
report := tfdriftcheck.Detect(plan, tfdriftcheck.Options{Ignore: []string{"aws_instance.*:tags.Env"}})
tfdriftcheck.Render(os.Stdout, report, "text")
```

Run as a CLI:

```
$ terraform plan -refresh-only -out=plan && terraform show -json plan > plan.json
$ tf-drift-check plan.json --ignore 'aws_instance.*:tags.Name'
aws_instance.web modified: tags.Env
1 resource drifted
```

Ignoring the drifted attribute itself removes the resource from the report:

```
$ tf-drift-check plan.json --ignore 'aws_instance.*:tags.Env'
0 resources drifted
```

That command exits 0: the only drift was ignored.

## Installation

```
go install github.com/Trysac/u281-tf-drift-check/cmd/tf-drift-check@latest
```

As a library, the module is `github.com/Trysac/u281-tf-drift-check` and the package clause is
`tfdriftcheck`:

```
import tfdriftcheck "github.com/Trysac/u281-tf-drift-check"
```

Go >= 1.23, standard library only.

## Public API

| Identifier | Signature / shape |
|---|---|
| `Parse` | `func Parse(r io.Reader, maxBytes int64) (*Plan, error)` |
| `Plan` | `struct { FormatVersion string; ResourceDrift []ResourceChange }` |
| `Detect` | `func Detect(p *Plan, opts Options) Report` |
| `Options` | `struct { Ignore []string }` |
| `Report` | `struct { Drifts []Drift; Ignored int }` |
| `Drift` | `struct { Address, Type, Kind, Paths }`, JSON tags `address`, `type`, `kind`, `paths` |
| `Render` | `func Render(w io.Writer, r Report, format string) error` |
| `Error` | `struct { Code string; Msg string }`, implements `error` |
| `Run` | `func Run(args []string, stdout, stderr io.Writer) int` (CLI entry, used by `cmd/tf-drift-check`) |

### Input

`Parse` reads `terraform show -json` output. `format_version` must have major version 1.
`resource_drift` entries are read for `address`, `type`, `change.actions`, `change.before` and
`change.after`; numbers are decoded with `json.Decoder.UseNumber` so nothing is lost to float
rounding.

### Kinds and paths

A drifted resource is `deleted` when `change.actions` is `["delete"]` or `after` is null (with
an empty `Paths`); otherwise it is `modified`. `Detect` walks `before` and `after` recursively:
objects by sorted key union, arrays by index up to the longer length, scalars by value. Numbers
are compared as exact decimals (`big.Rat.SetString`), strings and bools exactly, and a type
change counts as a change. Each difference is reported at the deepest differing node.

Path grammar: keys matching `^[A-Za-z_][A-Za-z0-9_-]*$` are joined with `.`; other keys are
written as `["key"]` with Go `%q` quoting; array indices are `[n]`. Paths are sorted, and
drifts are sorted by address.

### Options and ignore rules

`Options.Ignore` holds rules of the form `<address glob>:<path glob>`, each side matched with
`path.Match` semantics, so `*` does not cross `.`. `tags.*` ignores every tag. A drift reduced
to no remaining paths is dropped from `Report.Drifts` and counted in `Report.Ignored`.

### Output

`Render` accepts `"text"` or `"json"`. Text writes one line `<address> <kind>: <paths joined by ", ">`
(deleted resources write `<address> deleted`) followed by `<n> resource drifted` or
`<n> resources drifted`. JSON writes `{"drifts":[...],"ignored":0}`. Values, `before_sensitive`
and `after_sensitive` contents never appear in output or in errors.

### Limits

- `maxBytes <= 0` means 52 428 800 bytes (50 MiB); at most `maxBytes+1` bytes are read.
- Decoded values may nest at most 64 levels deep.
- Error messages are at most 200 characters.

### Errors

`Parse` and `Detect` report problems as `*Error` with `Code` one of:

| Code | Meaning |
|---|---|
| `input_too_large` | input exceeded the byte limit |
| `invalid_json` | the input is not valid JSON |
| `unsupported_format` | `format_version` major is not 1 |
| `too_deep` | a value nests deeper than 64 |
| `invalid_rule` | an ignore rule has no `:` separator |

### CLI

`tf-drift-check <plan.json>` accepts `--format text|json` and a repeatable `--ignore rule`.
Exit status: 0 no drift, 1 drift found, 2 usage, I/O or parse error.

## Why not read the plan output directly

`terraform show -json` gives attribute paths only after you diff it yourself, and its
before/after blocks carry full values that may be sensitive. This library reports
attribute-level paths only, safe to paste into tickets and CI logs, with ignore rules for
known noisy attributes.

## Documentation

- [pkg.go.dev](https://pkg.go.dev/github.com/Trysac/u281-tf-drift-check) for the full API reference.
- [docs/PROJECT.md](docs/PROJECT.md) for objective, scope and out-of-scope.
- [docs/SECURITY.md](docs/SECURITY.md) for the threat model and limits.
- [docs/TESTING.md](docs/TESTING.md) for the tests.
