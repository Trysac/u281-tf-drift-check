# Testing

## Running

```sh
go test -cover .          # tests with coverage summary
go run ./scripts/check    # every gate, as CI and the factory run it
```

## What the tests cover

Tests live in package `tfdriftcheck_test` next to the code they exercise and use the public API
only. The behaviours asserted come from the specification's acceptance list:

- `Parse`: `input_too_large` for an input above the byte limit (52428801 bytes), `invalid_json`
  for `{`, `unsupported_format` for `format_version` `"2.0"`, and `too_deep` for values nested
deeper than 64. A plan without `resource_drift` yields no drifts and `Run` returns 0.
- `Detect` kinds: `change.actions` `["update"]` gives `modified`; `["delete"]` with `after`
  null gives `deleted` with empty `Paths`.
- `Detect` paths: added key (`a` before, `a` and `b` after gives `["b"]`), array element
  change (`x[1]`), array growth (`x[1]`), a map key containing a dot (`tags["a.b"]`), a key with
  a space, unicode keys, null versus missing key, and empty object versus missing key.
- Number comparison: `1` and `1.0` are equal; `9007199254740993` and `9007199254740992` differ.
- `Options{Ignore: []string{"aws_instance.*:tags.Env"}}` removes that path; a drift left with
  no paths is dropped and `Report.Ignored == 1`.
- `Render`: `text` writes exactly
  `"aws_instance.web modified: tags.Env\n1 resource drifted\n"` and never the marker string
  present in before/after; `json` writes `{"drifts":[...],"ignored":0}`.
- `Run`: returns 1 when drift is found, 0 when none, 2 for a missing file or unknown flag;
  `--ignore` may be repeated.
- Ordering: 100 drifted resources come out sorted by address.

## Policy

- Statement coverage of the library (every package except `scripts/`) must stay at or above 80 %;
  the gate fails below it.
- Tests assert every observable effect: return values, returned errors (`errors.Is`) and their
  messages.
- Prefer table-driven tests with `t.Run` subtests; document usage with runnable `Example`
  functions, which `go test` verifies through their `// Output:` comments.
- The race detector runs whenever cgo and a C compiler are available (always in Linux CI); the
  gate prints why when it has to run without it.
- Each bug fix starts with a failing test that reproduces it.
- Tests are deterministic: no network, no wall clock, no dependence on test order.

## Tiers

- Local tier (every gate run, coverage measured here): unit and property tests, plus conformance on small
  fixtures kept in the repository.
- CI tier: conformance that needs an external tool or a download. Start the file with `//go:build conformance`; `scripts/check` adds `-tags=conformance` only in CI
  (unless `FABRICA_CI=1`). The CI `conformance` job sets `FABRICA_CI=1` and runs the full suite. Tools are installed
  by the optional `ci/conformance-setup.sh` (bash, CI only; verify every download against a pinned SHA-256), and
  tagged tests skip cleanly when their tool is missing.

## Layout

`<file>_test.go` sits next to the file it tests. Fixtures and golden files go in `testdata/`.
