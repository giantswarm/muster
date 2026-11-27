package api

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"testing"
)

// The muster chart refuses at render what muster refuses at start, from its
// own copy of the built-in roles and floors: the copy must equal muster's.
func TestChartBuiltinRoleFloorsMatch(t *testing.T) {
	raw, err := os.ReadFile("../../helm/muster/templates/_helpers.tpl")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)define "muster\.governance\.builtinRoleFloors" -}}\s*(\{.*?\})\s*\{\{- end`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("muster.governance.builtinRoleFloors not found in the chart")
	}
	var chart map[string][]string
	if err := json.Unmarshal(m[1], &chart); err != nil {
		t.Fatal(err)
	}
	for _, role := range GovernanceRoles() {
		got, ok := chart[role]
		if !ok {
			t.Fatalf("the chart does not know the built-in role %s", role)
		}
		if !slices.Equal(got, builtinRoleFloors[role]) && (len(got) != 0 || len(builtinRoleFloors[role]) != 0) {
			t.Fatalf("role %s: chart floors %v, muster's %v", role, got, builtinRoleFloors[role])
		}
	}
	if len(chart) != len(GovernanceRoles()) {
		t.Fatalf("the chart lists %d roles, muster builds in %d", len(chart), len(GovernanceRoles()))
	}
}
