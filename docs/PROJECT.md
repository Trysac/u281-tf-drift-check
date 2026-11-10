# Project record

## Objective

Detect Terraform drift from the `resource_drift` section of plan JSON: attribute paths that
changed outside Terraform, deletions, ignore rules, values never printed.

## Scope

In scope, as implemented in the root package `tfdriftcheck` and the CLI in
`cmd/tf-drift-check`:

- `Parse(r io.Reader, maxBytes int64) (*Plan, error)` for `terraform show -json` output whose
  `format_version` major is 1, with a byte limit (default 52 428 800) and a nesting depth
  limit of 64.
- `Detect(p *Plan, opts Options) Report` classifying each `resource_drift` entry as `modified`
  or `deleted` and computing the deepest differing attribute paths between `change.before`
  and `change.after`, with exact decimal number comparison.
- `Options.Ignore` rules of the form `<address glob>:<path glob>` matched with `path.Match`
  semantics; fully ignored drifts are dropped and counted in `Report.Ignored`.
- `Render(w io.Writer, r Report, format string) error` for `text` and `json` output, and the
  `Run(args []string, stdout, stderr io.Writer) int` CLI delegating to it.
- A typed `*Error` with codes `input_too_large`, `invalid_json`, `unsupported_format`,
  `too_deep` and `invalid_rule`.

## Out of scope

- Running `terraform` or invoking any external process.
- Reading Terraform state files directly.
- The `resource_changes` section (planned changes); only `resource_drift` is inspected.
- Remediation of any kind.
- Any third-party dependency: the module is standard library only.

## Timeline

| Date | Event |
|---|---|
| 2026 | Repository created from the factory utility template. |

## Effort metrics

Measured values only; a value that has not been measured stays "pending measurement".

| Metric | Value |
|---|---|
| Tokens (input / output) | 1459606 / 85178 |
| Hours (human) | pending measurement |
| Cost (USD) | 0.1079 |

## Factory run u281 (measured from API usage)

| Date (UTC) | Phase | Model | Batch | Input | Output | Cache write | Cache read | Cost (USD) |
|---|---|---|---|---|---|---|---|---|
| 2026-11-10 | generation | deepseek-v4-pro | yes | 1525 | 28607 | 0 | 3328 | 0.0577 |
| 2026-11-10 | fix-1 | deepseek-flash | no | 26326 | 23807 | 0 | 770816 | 0.0205 |
| 2026-11-10 | audit | deepseek-flash | no | 27379 | 20605 | 0 | 246784 | 0.0172 |
| 2026-11-10 | fix-2 | deepseek-flash | no | 23300 | 8334 | 0 | 355712 | 0.0096 |
| 2026-11-10 | docs | deepseek-flash | no | 4052 | 3825 | 0 | 384 | 0.0029 |

Total: $0.1079. Prices: deepseek line, as measured by its adapter. Providers serve the current version of each model id, so the date tells which version answered; versions, launch stages and price sources per line are in the factory's model register (docs/MODELS.md).
