// Package tfdriftcheck is a small, dependency-free Go library: Detect Terraform drift from the resource_drift section of plan JSON: attribute paths that changed outside Terraform, deletions, ignore rules, values never printed
//
// The public API is every exported identifier of this root package. Functions are pure: they do
// no I/O, keep no global mutable state and report invalid input through returned errors.
package tfdriftcheck
