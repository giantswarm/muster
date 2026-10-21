package aggregator

import "github.com/mark3labs/mcp-go/mcp"

// RegisteredToolsForTest are the tools the aggregator serves: the meta-tools
// as createMetaToolsFromProvider builds them, and the core tools reached
// through call_tool (buildCoreTools).
func RegisteredToolsForTest() (meta, core []mcp.Tool) {
	a := &AggregatorServer{}
	for _, st := range a.createToolsFromProviders() {
		meta = append(meta, st.Tool)
	}
	core, _ = a.buildCoreTools()
	return meta, core
}
