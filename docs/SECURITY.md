# Security notes

## Threat model

`github.com/Trysac/u281-tf-drift-check` is a pure Go library (standard library only) plus a
thin CLI. It never runs `terraform`, never reads state files and never opens a network
connection.

### Defended

- **Sensitive values in plan JSON.** `change.before` and `change.after` may contain secrets.
  `Detect` reduces them to attribute paths and `Render` emits only addresses, kinds and paths;
  `before_sensitive` and `after_sensitive` contents are never read into output, and error
  messages never contain values. Error messages are capped at 200 characters.
- **Oversized or hostile input.** `Parse` reads at most `maxBytes+1` bytes and fails with
  `input_too_large` beyond the limit (default 52 428 800 bytes, i.e. 50 MiB). Values nested
  deeper than 64 levels fail with `too_deep` instead of exhausting the stack.
- **Malformed input.** Invalid JSON yields `invalid_json`; a `format_version` whose major is
  not 1 yields `unsupported_format`; a malformed ignore rule yields `invalid_rule`. The
  package returns `*Error` values and does not panic on input, print, or exit.
- **Number precision.** Numbers are compared as exact decimals via `big.Rat.SetString`, so a
  comparison cannot be fooled by float64 rounding (9007199254740993 vs 9007199254740992 are
  different).
- **Ignore rule scope.** Rules use `path.Match` semantics on the address and on the path
  separately, so `*` does not cross `.` and a rule cannot silently widen beyond its glob.
- **Supply chain.** No third-party dependencies, so there is no dependency surface beyond the
  Go standard library and toolchain.

### Not defended

- **Content of correctly parsed values.** The library does not classify or redact; it drops
  values from its own output, but a caller that prints `Plan` fields directly is outside its
  control. `Plan.ResourceDrift` retains the decoded entries.
- **Who may run the CLI or read its output.** Access control, log retention and terminal
  sharing are the operator's responsibility.
- **Terraform itself.** Producing a correct `-refresh-only` plan, provider behaviour and state
  storage are out of scope.
- **Resource drift outside `resource_drift`.** Only that section is inspected, so drift
  reported elsewhere in the plan is not seen.
- **Denial of service by many resources.** Sorting and diffing are linear in the plan size; a
  plan within the byte limit but with a very large number of resources is still processed.

## Reporting a vulnerability

Open a private security advisory on the repository rather than a public issue.
