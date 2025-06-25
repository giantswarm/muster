package commands

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

// GetCommand retrieves resources
type GetCommand struct {
	*BaseCommand
}

// NewGetCommand creates a new get command
func NewGetCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *GetCommand {
	return &GetCommand{
		BaseCommand: NewBaseCommand(client, output, transport),
	}
}

// Execute retrieves a resource
func (g *GetCommand) Execute(ctx context.Context, args []string) error {
	parsed, err := g.parseArgs(args, 1, g.Usage())
	if err != nil {
		return err
	}

	uri := parsed[0]

	g.output.Info("Retrieving resource: %s...", uri)

	// Get the resource
	result, err := g.client.GetResource(ctx, uri)
	if err != nil {
		g.output.Error("Failed to get resource: %v", err)
		return nil
	}

	// Display contents
	g.output.OutputLine("Contents:")
	for _, content := range result.Contents {
		if textContent, ok := content.(mcp.TextResourceContents); ok {
			// Try to format as JSON if possible
			var jsonObj interface{}
			if err := json.Unmarshal([]byte(textContent.Text), &jsonObj); err == nil {
				if b, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
					g.output.OutputLine(string(b))
				} else {
					g.output.OutputLine(textContent.Text)
				}
			} else {
				g.output.OutputLine(textContent.Text)
			}
		} else if blobContent, ok := content.(mcp.BlobResourceContents); ok {
			g.output.OutputLine("[Binary data: %d bytes]", len(blobContent.Blob))
		} else {
			g.output.OutputLine("%+v", content)
		}
	}

	return nil
}

// Usage returns the usage string
func (g *GetCommand) Usage() string {
	return "get <resource-uri>"
}

// Description returns the command description
func (g *GetCommand) Description() string {
	return "Retrieve a resource by URI"
}

// Completions returns possible completions
func (g *GetCommand) Completions(input string) []string {
	return g.getResourceCompletions()
}

// Aliases returns command aliases
func (g *GetCommand) Aliases() []string {
	return []string{"fetch"}
}
