// Package tfdriftcheck detects Terraform infrastructure drift from the
// `resource_drift` section of a `terraform show -json` plan output.
//
// It parses the plan JSON, computes attribute-level differences between the
// before and after state of each drifted resource, applies ignore rules for
// noisy attributes, and renders a concise report that never includes the
// values themselves. This makes drift output safe to share in tickets and
// CI logs.
//
// The primary entry points are [Parse], [Detect], and [Render].
package tfdriftcheck
