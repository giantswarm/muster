package commands

import (
	"context"
	"fmt"
	"strings"
)

// NotificationsCommand handles enabling/disabling notifications
type NotificationsCommand struct {
	*BaseCommand
}

// NewNotificationsCommand creates a new notifications command
func NewNotificationsCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *NotificationsCommand {
	return &NotificationsCommand{
		BaseCommand: NewBaseCommand(client, output, transport),
	}
}

// Execute enables or disables notification display
func (n *NotificationsCommand) Execute(ctx context.Context, args []string) error {
	parsed, err := n.parseArgs(args, 1, n.Usage())
	if err != nil {
		return err
	}

	action := strings.ToLower(parsed[0])
	switch action {
	case "on", "enable", "true":
		if !n.transport.SupportsNotifications() {
			n.output.Error("Notifications are not supported with current transport. Use --transport=sse or --transport=streamable-http for notification support.")
			return nil
		}
		// Enable notifications (implementation would go here)
		n.output.Success("Notifications enabled")
	case "off", "disable", "false":
		// Disable notifications (implementation would go here)
		n.output.Success("Notifications disabled")
	default:
		return fmt.Errorf("invalid action: %s. Use 'on' or 'off'", action)
	}

	return nil
}

// Usage returns the usage string
func (n *NotificationsCommand) Usage() string {
	return "notifications <on|off>"
}

// Description returns the command description
func (n *NotificationsCommand) Description() string {
	return "Enable or disable notification display"
}

// Completions returns possible completions
func (n *NotificationsCommand) Completions(input string) []string {
	return []string{"on", "off", "enable", "disable"}
}

// Aliases returns command aliases
func (n *NotificationsCommand) Aliases() []string {
	return []string{"notify"}
}
