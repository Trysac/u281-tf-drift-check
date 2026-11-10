# Use recursive diff and path grammar for drift detection

Status: accepted (generated, pending human review)

## Context

The library needs to extract meaningful drift information from Terraform plan JSON without leaking sensitive values. Attribute paths provide a safe, concise representation.

## Decision

Implement a recursive diff algorithm that compares before/after values and produces a list of paths at the deepest differing node. Paths follow a grammar: simple keys dot-separated, other keys quoted in brackets, array indices in brackets. Ignore rules use glob matching with path.Match semantics where '.' is treated as a separator.

## Consequences

The approach yields precise, shareable drift summaries. It requires careful handling of missing vs. null values, number precision, and path construction. The path grammar is simple but supports arbitrary keys.
