package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/config"
)

// Test tools that measure what a call costs and what the store holds, for
// the budgets an installation-shaped scenario asserts (pre_configuration.
// fixture: scale). Neither changes anything; both report numbers a step
// bounds with json_path_max.
const (
	// TestToolMeasureMetaTool calls a meta-tool as the current user, once or
	// repeatedly, and reports its wall-clock duration, the bytes of its
	// response and the Valkey commands the instance issued while it ran.
	TestToolMeasureMetaTool = "test_measure_meta_tool"
	// TestToolValkeyFootprint reports what the instance's Valkey stand-in
	// holds: keys and bytes by prefix, the sessions with capability entries
	// and the capability bytes per session.
	TestToolValkeyFootprint = "test_valkey_footprint"
)

// measureRepeatMax bounds test_measure_meta_tool's repeat argument.
const measureRepeatMax = 50

// handleMeasureMetaTool calls the meta-tool `tool` with `arguments`, `repeat`
// times (default 1), through the current user's session, and reports:
// duration_ms (the median), duration_ms_min, duration_ms_max, response_bytes
// (the text content of the last response), valkey_commands (the fewest
// commands the instance's Valkey stand-in saw during one call -- the floor
// a budget is about; background work may add to single runs) and
// valkey_commands_max, both only with storage.type: valkey, and response
// (the last response decoded, or its text).
func (h *TestToolsHandler) handleMeasureMetaTool(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	toolName, ok := args["tool"].(string)
	if !ok || toolName == "" {
		return nil, fmt.Errorf("tool argument is required")
	}
	toolArgs, _ := args["arguments"].(map[string]interface{})
	repeat := 1
	if raw, present := args["repeat"]; present {
		n, ok := numericValue(raw)
		if !ok || n < 1 || n > measureRepeatMax {
			return nil, fmt.Errorf("repeat must be a number between 1 and %d, got %v", measureRepeatMax, raw)
		}
		repeat = int(n)
	}
	client := h.GetCurrentClient()
	if client == nil {
		return nil, fmt.Errorf("no MCP client available")
	}
	valkey := h.instanceValkey()

	durations := make([]time.Duration, 0, repeat)
	var commands []int
	fewest, fewestByName := -1, map[string]int(nil)
	var lastText string
	for i := 0; i < repeat; i++ {
		before := commandCountsOf(valkey)
		start := time.Now()
		result, err := client.CallToolDirect(ctx, toolName, toolArgs)
		took := time.Since(start)
		after := commandCountsOf(valkey)
		if err != nil {
			return nil, fmt.Errorf("meta-tool %s call failed: %w", toolName, err)
		}
		lastText = textContent(result)
		if result.IsError {
			return nil, fmt.Errorf("meta-tool %s returned error: %s", toolName, lastText)
		}
		durations = append(durations, took)
		if valkey != nil {
			byName, total := commandDelta(before, after)
			if fewest < 0 || total < fewest {
				fewest, fewestByName = total, byName
			}
			commands = append(commands, total)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	out := map[string]interface{}{
		"tool":            toolName,
		"repeat":          repeat,
		"duration_ms":     milliseconds(durations[len(durations)/2]),
		"duration_ms_min": milliseconds(durations[0]),
		"duration_ms_max": milliseconds(durations[len(durations)-1]),
		"response_bytes":  len(lastText),
		"response":        decodedOrText(lastText),
	}
	if len(commands) > 0 {
		sort.Ints(commands)
		out["valkey_commands"] = commands[0]
		out["valkey_commands_max"] = commands[len(commands)-1]
		byName := make(map[string]interface{}, len(fewestByName))
		for name, n := range fewestByName {
			byName[name] = n
		}
		out["valkey_commands_by_name"] = byName
	}
	if h.debug {
		h.logger.Debug("📏 %s ×%d: %v ms median, %d bytes, valkey commands %v\n", toolName, repeat, out["duration_ms"], len(lastText), commands)
	}
	return out, nil
}

// handleValkeyFootprint walks every key of the instance's Valkey stand-in and
// reports keys, fields and bytes (key, field names and values) by the
// segment after muster's key prefix -- cap, capblob, auth, token, ... -- plus
// the capability store's shape: sessions (the cap:<session> hashes),
// capability_entries (their fields), capability_documents (the content-
// addressed capblob:<digest> documents), inline_capability_entries (fields
// that carry a document instead of a reference), capability_bytes (cap and
// capblob together) and capability_bytes_per_session. Requires
// storage.type: valkey.
func (h *TestToolsHandler) handleValkeyFootprint(_ context.Context, _ map[string]interface{}) (interface{}, error) {
	valkey := h.instanceValkey()
	if valkey == nil {
		return nil, fmt.Errorf("%s needs pre_configuration.storage.type: valkey", TestToolValkeyFootprint)
	}
	return valkey.footprint(config.DefaultValkeyKeyPrefix), nil
}

// instanceValkey returns the current instance's Valkey stand-in, nil without one.
func (h *TestToolsHandler) instanceValkey() *instanceValkey {
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil
	}
	return h.instanceManager.valkeyFor(h.currentInstance.ID)
}

// commandCounts is nil-safe: a nil stand-in has processed nothing.
func commandCountsOf(v *instanceValkey) map[string]int {
	if v == nil {
		return nil
	}
	return v.commandCounts()
}

// commandDelta is what the stand-in processed between two snapshots, by
// command name and in total.
func commandDelta(before, after map[string]int) (map[string]int, int) {
	delta := map[string]int{}
	total := 0
	for name, n := range after {
		if d := n - before[name]; d > 0 {
			delta[name] = d
			total += d
		}
	}
	return delta, total
}

// prefixFootprint is what one key prefix holds.
type prefixFootprint struct {
	Keys   int `json:"keys"`
	Fields int `json:"fields"`
	Bytes  int `json:"bytes"`
}

// footprint measures the stand-in's content by key prefix segment.
func (v *instanceValkey) footprint(keyPrefix string) map[string]interface{} {
	prefixes := map[string]*prefixFootprint{}
	totalKeys, totalBytes := 0, 0
	sessions, entries, inlineEntries, documents := 0, 0, 0, 0
	for _, key := range v.srv.Keys() {
		segment := strings.TrimPrefix(key, keyPrefix)
		if i := strings.Index(segment, ":"); i >= 0 {
			segment = segment[:i]
		}
		fp := prefixes[segment]
		if fp == nil {
			fp = &prefixFootprint{}
			prefixes[segment] = fp
		}
		fields, size := v.keySize(key)
		fp.Keys++
		fp.Fields += len(fields)
		fp.Bytes += size
		totalKeys++
		totalBytes += size
		switch segment {
		case "cap":
			sessions++
			entries += len(fields)
			for _, value := range fields {
				if !strings.HasPrefix(value, "sha256:") {
					inlineEntries++
				}
			}
		case "capblob":
			documents++
		}
	}
	capabilityBytes := 0
	for _, segment := range []string{"cap", "capblob"} {
		if fp := prefixes[segment]; fp != nil {
			capabilityBytes += fp.Bytes
		}
	}
	perSession := 0.0
	if sessions > 0 {
		perSession = float64(capabilityBytes) / float64(sessions)
	}
	byPrefix := make(map[string]interface{}, len(prefixes))
	for segment, fp := range prefixes {
		byPrefix[segment] = map[string]interface{}{"keys": fp.Keys, "fields": fp.Fields, "bytes": fp.Bytes}
	}
	return map[string]interface{}{
		"keys":                         totalKeys,
		"bytes":                        totalBytes,
		"prefixes":                     byPrefix,
		"sessions":                     sessions,
		"capability_entries":           entries,
		"inline_capability_entries":    inlineEntries,
		"capability_documents":         documents,
		"capability_bytes":             capabilityBytes,
		"capability_bytes_per_session": perSession,
	}
}

// keySize returns a key's hash fields (name -> value, empty for other types)
// and the bytes it holds: the key itself plus its field names and values or
// its value, members or elements.
func (v *instanceValkey) keySize(key string) (map[string]string, int) {
	size := len(key)
	fields := map[string]string{}
	switch v.srv.Type(key) {
	case "hash":
		names, _ := v.srv.HKeys(key)
		for _, name := range names {
			value := v.srv.HGet(key, name)
			fields[name] = value
			size += len(name) + len(value)
		}
	case "string":
		value, _ := v.srv.Get(key)
		size += len(value)
	case "set":
		members, _ := v.srv.Members(key)
		for _, m := range members {
			size += len(m)
		}
	case "list":
		items, _ := v.srv.List(key)
		for _, it := range items {
			size += len(it)
		}
	case "zset":
		members, _ := v.srv.ZMembers(key)
		for _, m := range members {
			size += len(m)
		}
	}
	return fields, size
}

func textContent(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if tc, ok := mcp.AsTextContent(content); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String()
}

// decodedOrText returns a JSON object decoded, anything else as text, the
// way test_call_meta_tool reports a meta-tool's answer.
func decodedOrText(text string) interface{} {
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return text
	}
	return parsed
}

func milliseconds(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}
