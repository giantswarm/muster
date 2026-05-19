package aggregator

import (
	"github.com/mark3labs/mcp-go/mcp"
)

func callRequest(name string) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name}}
}
