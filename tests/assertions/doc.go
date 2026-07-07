// Package assertions holds framework-agnostic checks on a deployed muster
// installation, shared by the ATS harness (tests/ats, chart-level tests on
// kind) and the apptest-framework harness (tests/e2e, workload-cluster
// tests). Functions take a controller-runtime client plus a Target and
// return an error, so each harness adapts them to its own reporting.
package assertions
