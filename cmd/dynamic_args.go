package cmd

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// parseDynamicArgs collects the parameters a command forwards to a tool or a
// workflow from args, the command line after the binary name (os.Args[1:]):
// every flag the command does not declare, and everything after a "--".
//
// cobra parses these commands with unknown flags allowed and drops them, so
// the parameters are read from the command line a second time. The flags the
// CLI declares -- --endpoint, --auth, --output, --json, ... -- are recognised
// through cmd.Flags(), the set cobra parsed, and skipped together with the
// token pflag took as their value: "--endpoint http://..." takes the next
// token, "--endpoint=http://..." and a boolean flag take none. A flag added to
// cli.RegisterCommonFlags is consumed by the CLI without a second list to keep
// in step.
//
// An undeclared "--key" is a parameter: "--key=value", "--key value" (the next
// token, unless it is a flag) or "--key" alone, which coerce turns into the
// value for "true". After "--" every "--key" is a parameter, declared or not,
// so a workflow argument named like a CLI flag can still be passed:
//
//	muster start workflow w --endpoint http://muster:8090/mcp -- --endpoint=https://target
//
// The other tokens -- the subcommand, positional arguments, shorthands such as
// "-o json" -- are cobra's and are left alone.
func parseDynamicArgs(cmd *cobra.Command, args []string, coerce func(string) interface{}) map[string]interface{} {
	params := make(map[string]interface{})
	verbatim := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" && !verbatim {
			verbatim = true
			continue
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}

		name, value, inline := strings.Cut(arg[2:], "=")
		if !verbatim {
			if flag := cmd.Flags().Lookup(name); flag != nil {
				if !inline && flag.NoOptDefVal == "" {
					i++ // "--flag value": pflag took the next token as the value
				}
				continue
			}
		}
		if name == "" {
			continue
		}
		if !inline {
			value = stringTrue
			if i+1 < len(args) && isValueToken(args[i+1]) {
				i++
				value = args[i]
			}
		}
		params[name] = coerce(value)
	}

	return params
}

// isValueToken reports whether token can follow "--key" as its value: anything
// that is not itself a flag. A negative number is a value, not a flag.
func isValueToken(token string) bool {
	if !strings.HasPrefix(token, "-") {
		return true
	}
	_, err := strconv.ParseFloat(token, 64)
	return err == nil
}

// stringValue keeps a parameter the string the command line spelled, the way
// `muster start workflow` passes workflow arguments.
func stringValue(s string) interface{} { return s }
