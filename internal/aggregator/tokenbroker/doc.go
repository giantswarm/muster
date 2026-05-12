// Package tokenbroker contains the aggregator's local adapters for the
// broker bounded context.
//
// Adapters wrap broker domain types into the consumer-defined
// [aggregator.TokenBroker] port, so aggregator code only crosses the seam
// through this subpackage.
package tokenbroker
