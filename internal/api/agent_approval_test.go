package api

import (
	"context"
	"testing"
)

func TestAgentApproval_Decide(t *testing.T) {
	github := AgentTarget{Kind: AgentTargetTool, Server: "github", Original: "merge_pull_request", Exposed: "x_github_merge_pull_request"}
	resolve := func(name string) (string, string, bool) {
		if name == "x_gh_merge_pull_request" {
			return "github", "merge_pull_request", true
		}
		return "", "", false
	}
	aliased := github
	aliased.ResolveTool = resolve
	workflow := AgentTarget{Kind: AgentTargetWorkflow, Workflow: "release", Exposed: "workflow_release"}
	core := AgentTarget{Kind: AgentTargetCore, Exposed: "core_service_stop"}
	member := AgentTarget{Kind: AgentTargetTool, Server: "k8s-a", Family: "k8s", Original: "get", Exposed: "x_k8s_get"}

	cases := []struct {
		name        string
		approval    *AgentApproval
		target      AgentTarget
		wantSetting string
		wantEntry   string
	}{
		{"nil settings", nil, github, "", ""},
		{"nothing selects", &AgentApproval{Entries: []AgentApprovalEntry{{"server:pagerduty", ApprovalAsk}}}, github, "", ""},
		{"default applies", &AgentApproval{Entries: []AgentApprovalEntry{{"server:pagerduty", ApprovalAsk}}, Default: ApprovalBlock}, github, ApprovalBlock, AgentDefaultEntry},
		{"server entry", &AgentApproval{Entries: []AgentApprovalEntry{{"server:github", ApprovalAsk}}, Default: ApprovalBlock}, github, ApprovalAsk, "server:github"},
		{"tool beats server", &AgentApproval{Entries: []AgentApprovalEntry{{"server:github", ApprovalBlock}, {"tool:x_github_merge_pull_request", ApprovalAllow}}}, github, ApprovalAllow, "tool:x_github_merge_pull_request"},
		{"alias selects the dispatch target", &AgentApproval{Entries: []AgentApprovalEntry{{"tool:x_gh_merge_pull_request", ApprovalBlock}}}, aliased, ApprovalBlock, "tool:x_gh_merge_pull_request"},
		{"alias without resolver", &AgentApproval{Entries: []AgentApprovalEntry{{"tool:x_gh_merge_pull_request", ApprovalBlock}}}, github, "", ""},
		{"workflow entry", &AgentApproval{Entries: []AgentApprovalEntry{{"workflow:release", ApprovalAsk}}}, workflow, ApprovalAsk, "workflow:release"},
		{"tool entry names a workflow by its core alias", &AgentApproval{Entries: []AgentApprovalEntry{{"tool:core_workflow_release", ApprovalBlock}, {"workflow:release", ApprovalAsk}}}, workflow, ApprovalBlock, "tool:core_workflow_release"},
		{"core tool without prefix", &AgentApproval{Entries: []AgentApprovalEntry{{"tool:service_stop", ApprovalBlock}}}, core, ApprovalBlock, "tool:service_stop"},
		{"server entry never selects a core tool", &AgentApproval{Entries: []AgentApprovalEntry{{"server:core", ApprovalBlock}}}, core, "", ""},
		{"family member by member", &AgentApproval{Entries: []AgentApprovalEntry{{"server:k8s-a", ApprovalAsk}}}, member, ApprovalAsk, "server:k8s-a"},
		{"family member by family", &AgentApproval{Entries: []AgentApprovalEntry{{"server:k8s", ApprovalBlock}}}, member, ApprovalBlock, "server:k8s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setting, entry := tc.approval.Decide(context.Background(), tc.target)
			if setting != tc.wantSetting || entry != tc.wantEntry {
				t.Fatalf("Decide = (%q, %q), want (%q, %q)", setting, entry, tc.wantSetting, tc.wantEntry)
			}
		})
	}
}

func TestAgentApproval_DecidePreset(t *testing.T) {
	t.Cleanup(func() { RegisterPresetSelector(nil) })
	RegisterPresetSelector(func(_ context.Context, preset string, target AgentTarget) bool {
		return preset == "writes" && target.Original == "merge_pull_request"
	})
	target := AgentTarget{Kind: AgentTargetTool, Server: "github", Original: "merge_pull_request", Exposed: "x_github_merge_pull_request"}
	a := &AgentApproval{Entries: []AgentApprovalEntry{{"preset:writes", ApprovalAsk}}, Default: ApprovalAllow}
	if setting, entry := a.Decide(context.Background(), target); setting != ApprovalAsk || entry != "preset:writes" {
		t.Fatalf("preset entry: got (%q, %q)", setting, entry)
	}
	a.Entries = append(a.Entries, AgentApprovalEntry{"server:github", ApprovalBlock})
	if setting, entry := a.Decide(context.Background(), target); setting != ApprovalBlock || entry != "server:github" {
		t.Fatalf("server beats preset: got (%q, %q)", setting, entry)
	}
}

func TestAgentApproval_StrictestOfEquallySpecific(t *testing.T) {
	target := AgentTarget{Kind: AgentTargetTool, Server: "k8s-a", Family: "k8s", Original: "get", Exposed: "x_k8s_get"}
	a := &AgentApproval{Entries: []AgentApprovalEntry{{"server:k8s", ApprovalAllow}, {"server:k8s-a", ApprovalAsk}}}
	if setting, _ := a.Decide(context.Background(), target); setting != ApprovalAsk {
		t.Fatalf("got %q, want the stricter Ask", setting)
	}
}

func TestApplyAgentSetting(t *testing.T) {
	toolAsk := ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceTool}
	ruleAsk := ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceConnector, New: true}
	allow := ToolApproval{Setting: ApprovalSettingAllow, Source: ApprovalSourceTool}
	unreviewed := ToolApproval{Setting: ApprovalSettingAllow, Source: ApprovalSourceUnreviewed}
	block := ToolApproval{Setting: ApprovalSettingBlock, Source: ApprovalSourceTool}
	builtin := ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceBuiltin}
	unavailable := ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceUnavailable}

	cases := []struct {
		name    string
		admin   ToolApproval
		setting string
		granted bool
		want    string
		source  string
	}{
		{"no agent setting", toolAsk, "", false, ApprovalSettingAsk, ApprovalSourceTool},
		{"unmarked Allow gives way to an Ask", toolAsk, ApprovalAllow, false, ApprovalSettingAsk, ApprovalSourceTool},
		{"granted Allow over a tool Ask", toolAsk, ApprovalAllow, true, ApprovalSettingAllow, ApprovalSourceAgentException},
		{"granted Allow over a rule Ask", ruleAsk, ApprovalAllow, true, ApprovalSettingAllow, ApprovalSourceAgentException},
		{"granted Allow never over Block", block, ApprovalAllow, true, ApprovalSettingBlock, ApprovalSourceTool},
		{"granted Allow never over builtin", builtin, ApprovalAllow, true, ApprovalSettingAsk, ApprovalSourceBuiltin},
		{"granted Allow never over unavailable", unavailable, ApprovalAllow, true, ApprovalSettingAsk, ApprovalSourceUnavailable},
		{"agent Ask over Allow", allow, ApprovalAsk, false, ApprovalSettingAsk, ApprovalSourceAgent},
		{"agent Ask over unreviewed", unreviewed, ApprovalAsk, false, ApprovalSettingAsk, ApprovalSourceAgent},
		{"agent Ask ties admin Ask", toolAsk, ApprovalAsk, false, ApprovalSettingAsk, ApprovalSourceTool},
		{"agent Block over Allow", allow, ApprovalBlock, false, ApprovalSettingBlock, ApprovalSourceAgent},
		{"agent Block over a granted case", toolAsk, ApprovalBlock, true, ApprovalSettingBlock, ApprovalSourceAgent},
		{"admin Block over agent Allow", block, ApprovalAllow, false, ApprovalSettingBlock, ApprovalSourceTool},
		{"unknown agent value counts as Ask", allow, "Deny", false, ApprovalSettingAsk, ApprovalSourceAgent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyAgentSetting(tc.admin, tc.setting, "tool:x", tc.granted)
			if got.Setting != tc.want || got.Source != tc.source {
				t.Fatalf("got %+v, want %s from %s", got, tc.want, tc.source)
			}
			if tc.setting != "" && (got.Entry != "tool:x" || got.Admin == nil || got.Admin.Setting != tc.admin.Setting) {
				t.Fatalf("entry and admin not reported: %+v", got)
			}
		})
	}
}

func TestAgentApproval_Granted(t *testing.T) {
	a := &AgentApproval{Grants: []AgentApprovalGrant{{Entry: "tool:x", Server: "github", UID: "u1"}}}
	if !a.Granted("tool:x", "github", "u1") {
		t.Fatal("matching grant not found")
	}
	for _, c := range [][3]string{{"tool:y", "github", "u1"}, {"tool:x", "gitlab", "u1"}, {"tool:x", "github", "u2"}, {"tool:x", "github", ""}} {
		if a.Granted(c[0], c[1], c[2]) {
			t.Fatalf("grant matched %v", c)
		}
	}
}

func TestAgentApproval_Validate(t *testing.T) {
	ok := &AgentApproval{
		Entries: []AgentApprovalEntry{{"server:github", ApprovalAsk}, {"tool:x_github_merge", ApprovalAllow}},
		Default: ApprovalAllow,
		Grants:  []AgentApprovalGrant{{"tool:x_github_merge", "github", "u"}, {AgentDefaultEntry, "github", "u"}},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid settings refused: %v", err)
	}
	bad := []*AgentApproval{
		{Entries: []AgentApprovalEntry{{"github", ApprovalAsk}}},
		{Entries: []AgentApprovalEntry{{"server:github", "ask"}}},
		{Entries: []AgentApprovalEntry{{"server:github", ApprovalAsk}, {"server:github", ApprovalBlock}}},
		{Default: "Never"},
		{Entries: []AgentApprovalEntry{{"server:github", ApprovalAsk}}, Grants: []AgentApprovalGrant{{"server:github", "github", "u"}}},
		{Grants: []AgentApprovalGrant{{AgentDefaultEntry, "github", "u"}}},
		{Entries: []AgentApprovalEntry{{"tool:x", ApprovalAllow}}, Grants: []AgentApprovalGrant{{"tool:x", "", "u"}}},
		{Entries: []AgentApprovalEntry{{"tool:x", ApprovalAllow}}, Grants: []AgentApprovalGrant{{"tool:x", "s", ""}}},
	}
	for i, a := range bad {
		if err := a.Validate(); err == nil {
			t.Fatalf("case %d: invalid settings accepted: %+v", i, a)
		}
	}
}
