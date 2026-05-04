// Package admin provides a read-only web UI for inspecting and managing
// muster sessions. It runs on a dedicated HTTP listener (configured via
// AggregatorConfig.Admin) without authentication; callers are expected to
// bind it to a loopback address and reach it via kubectl port-forward or
// localhost.
//
// The package is intentionally minimal: plain HTML templates, a sprinkle of
// CSS, no JavaScript framework, no persistent state of its own. All data is
// fetched through the Deps callbacks supplied by the aggregator.
package admin
