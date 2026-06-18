// Package admin provides a read-only web UI for inspecting and managing
// muster sessions. It runs on a dedicated HTTP listener (configured via
// AggregatorConfig.Admin). When an AuthMiddleware is supplied (the aggregator
// wires in OAuth validation when the OAuth server is enabled) every route is
// protected; otherwise the listener may only bind a loopback address.
//
// The package is intentionally minimal: plain HTML templates, a sprinkle of
// CSS, no JavaScript framework, no persistent state of its own. All data is
// fetched through the Deps callbacks supplied by the aggregator.
package admin
