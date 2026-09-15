//go:build race

package aggregator

// raceDetectorEnabled reports whether the binary runs under the race detector,
// which slows every call several times over: the duration budgets are
// measured and asserted without it (the count budgets hold either way).
const raceDetectorEnabled = true
