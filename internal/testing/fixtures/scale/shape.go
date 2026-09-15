package scale

// The shape: every number and every name the generator draws from. Nothing
// here is an installation's -- installations are minerals, tools are generic
// verbs over each family's nouns, people are numbered.

// seed makes the generator's choices reproducible; changing it changes the
// committed fixture.
const seed = 20260915

const (
	musterOAuthServer = "fixture-idp"
	fleetIssuer       = "fleet-idp"
	instanceArg       = "installation"
	workflowCount     = 282
	sessionCount      = 450
	personCount       = 60
	// installationsPerFamily caps how many installations a family spans;
	// the largest family is on every installation.
	maxInstallations = 28
)

// Property names and descriptions several tools share: an argument means the
// same thing wherever a family's tools take it.
const (
	propCluster  = "cluster"
	propWindow   = "window"
	propQuery    = "query"
	propSelector = "selector"
	propPeriod   = "period"
	propTicket   = "ticket"

	descCluster        = "Name of the cluster."
	descStreamSelector = "Stream selector."
	descWindow         = "Window to look back, for example 24h."
	descBillingPeriod  = "Billing period."
	descTicket         = "Ticket identifier."
	descQuery          = "Free-text query."
)

// installations are the invented management installations the family members
// run on; a family with n members is on the first n.
var installations = []string{
	"amber", "basalt", "cobalt", "dunite", "epidote", "feldspar", "garnet",
	"halite", "iolite", "jasper", "kyanite", "lazurite", "marble", "nephrite",
	"onyx", "pumice", "quartz", "rutile", "sodalite", "topaz", "ulexite",
	"verdite", "wulfenite", "xenotime", "yttria", "zircon", "agate", "beryl",
}

// familyShape is one family of equivalent servers: one per installation, the
// same tool catalogue everywhere, differing only by how many of the catalogue's
// tools a member's version offers (the variants are the distinct capability
// documents).
type familyShape struct {
	name     string
	members  int
	variants int
	// noun is what the family's tools act on, for descriptions.
	subject string
	tools   []toolShape
}

// toolShape is one tool of a family's catalogue.
type toolShape struct {
	name        string
	description string
	props       []Property
	required    []string
	readOnly    bool
}

var families = []familyShape{
	{
		name: "clusters", members: 28, variants: 4, subject: "workload clusters",
		tools: []toolShape{
			{"list_clusters", "List the workload clusters of the installation with their release, node count and readiness.", []Property{{"organization", "Restrict the list to the clusters of one organization."}, {"phase", "Restrict the list to clusters in one lifecycle phase."}}, nil, true},
			{"get_cluster", "Describe one workload cluster: release, node pools, conditions and the last transition of each.", []Property{{propCluster, descCluster}}, []string{propCluster}, true},
			{"list_node_pools", "List the node pools of a cluster with their instance type, size and autoscaling bounds.", []Property{{propCluster, descCluster}}, []string{propCluster}, true},
			{"get_release", "Describe a release: the component versions it ships and the clusters running it.", []Property{{"release", "Release version, for example 30.1.0."}}, []string{"release"}, true},
			{"list_upgrades", "List the upgrades scheduled or in progress on the installation.", []Property{{propCluster, "Restrict to one cluster."}, {propWindow, "Only upgrades scheduled within this duration, for example 72h."}}, nil, true},
			{"get_quota", "Report the installation's quota usage: clusters, nodes and volumes against the contracted limits.", []Property{{"organization", "Restrict to one organization."}}, nil, true},
			{"scale_node_pool", "Change the size of a node pool.", []Property{{propCluster, descCluster}, {"node_pool", "Name of the node pool."}, {"replicas", "Desired number of nodes."}}, []string{propCluster, "node_pool", "replicas"}, false},
			{"schedule_upgrade", "Schedule a cluster upgrade to a release at a time.", []Property{{propCluster, descCluster}, {"release", "Target release version."}, {"at", "RFC 3339 time to start the upgrade."}}, []string{propCluster, "release"}, false},
			{"list_events", "List the platform events of a cluster in the given window.", []Property{{propCluster, descCluster}, {propWindow, descWindow}}, []string{propCluster}, true},
		},
	},
	{
		name: "metrics", members: 20, variants: 4, subject: "time series",
		tools: []toolShape{
			{"query_instant", "Evaluate a query at one instant and return the resulting samples.", []Property{{propQuery, "The query expression."}, {"at", "RFC 3339 evaluation time; now when absent."}}, []string{propQuery}, true},
			{"query_range", "Evaluate a query over a window with a step and return the series.", []Property{{propQuery, "The query expression."}, {propWindow, "Window to look back, for example 6h."}, {"step", "Resolution step, for example 1m."}}, []string{propQuery}, true},
			{"list_series", "List the series matching a selector, with their labels.", []Property{{propSelector, "A series selector."}, {"limit", "Maximum number of series to return."}}, []string{propSelector}, true},
			{"list_label_values", "List the values a label takes across the matching series.", []Property{{"label", "Label name."}, {propSelector, "Restrict to series matching this selector."}}, []string{"label"}, true},
			{"list_targets", "List the scrape targets with their health and last scrape.", []Property{{"job", "Restrict to one scrape job."}}, nil, true},
			{"list_rules", "List the recording and alerting rules with their health and last evaluation.", []Property{{"group", "Restrict to one rule group."}}, nil, true},
			{"explain_series", "Describe where a series comes from: the job, the exporter and the rule that records it.", []Property{{"series", "The series name."}}, []string{"series"}, true},
		},
	},
	{
		name: "alerts", members: 16, variants: 4, subject: "alerts",
		tools: []toolShape{
			{"list_alerts", "List the alerts currently firing or pending with their labels and duration.", []Property{{"state", "Restrict to firing or pending alerts."}, {"severity", "Restrict to one severity."}}, nil, true},
			{"get_alert", "Describe one alert: its labels, annotations, the rule behind it and the silences that match it.", []Property{{"fingerprint", "The alert's fingerprint."}}, []string{"fingerprint"}, true},
			{"list_silences", "List the silences with their matchers, creator and expiry.", []Property{{"state", "Restrict to active, pending or expired silences."}}, nil, true},
			{"create_silence", "Silence the alerts matching the given matchers until a time.", []Property{{"matchers", "Label matchers, comma separated."}, {"until", "RFC 3339 expiry time."}, {"comment", "Why the alerts are silenced."}}, []string{"matchers", "until", "comment"}, false},
			{"expire_silence", "Expire a silence now.", []Property{{"silence", "The silence's identifier."}}, []string{"silence"}, false},
			{"list_routes", "List the notification routes and the receivers they deliver to.", nil, nil, true},
		},
	},
	{
		name: "logs", members: 12, variants: 3, subject: "log streams",
		tools: []toolShape{
			{"query_logs", "Return the log lines matching a selector in a window.", []Property{{propSelector, descStreamSelector}, {propWindow, "Window to look back, for example 1h."}, {"limit", "Maximum number of lines."}}, []string{propSelector}, true},
			{"list_streams", "List the log streams matching a selector, with their labels and volume.", []Property{{propSelector, descStreamSelector}}, nil, true},
			{"count_lines", "Count the matching log lines per interval over a window.", []Property{{propSelector, descStreamSelector}, {propWindow, "Window to look back."}, {"interval", "Bucket size, for example 5m."}}, []string{propSelector}, true},
			{"detect_patterns", "Cluster the matching log lines into patterns and return each pattern with its count.", []Property{{propSelector, descStreamSelector}, {propWindow, "Window to look back."}}, []string{propSelector}, true},
			{"get_retention", "Report the retention applied to the matching streams.", []Property{{propSelector, descStreamSelector}}, nil, true},
			{"tail_stream", "Return the newest lines of a stream.", []Property{{propSelector, descStreamSelector}, {"lines", "Number of lines."}}, []string{propSelector}, true},
		},
	},
	{
		name: "ledger", members: 8, variants: 3, subject: "usage records",
		tools: []toolShape{
			{"list_usage", "List the metered usage of the installation per cluster and day in a window.", []Property{{propWindow, "Window to look back, for example 720h."}, {propCluster, "Restrict to one cluster."}}, nil, true},
			{"get_statement", "Return the statement of a billing period.", []Property{{propPeriod, "Billing period, for example 2026-08."}}, []string{propPeriod}, true},
			{"list_adjustments", "List the manual adjustments booked against the installation.", []Property{{propPeriod, "Restrict to one billing period."}}, nil, true},
			{"book_adjustment", "Book a manual adjustment against a billing period.", []Property{{propPeriod, descBillingPeriod}, {"amount", "Signed amount in the contract's currency."}, {"reason", "Why the adjustment is booked."}}, []string{propPeriod, "amount", "reason"}, false},
			{"export_records", "Export the raw usage records of a period.", []Property{{propPeriod, descBillingPeriod}, {"format", "csv or json."}}, []string{propPeriod}, true},
		},
	},
}

// inHouseServers are the servers without per-session authentication whose
// tools every session sees; not family members.
var inHouseServers = []struct {
	name  string
	tools []toolShape
}{
	{"handbook", []toolShape{
		{"search_pages", "Search the handbook pages and return the matching sections with their paths.", []Property{{propQuery, descQuery}, {"limit", "Maximum number of sections."}}, []string{propQuery}, true},
		{"read_page", "Return one handbook page as text.", []Property{{"path", "Path of the page."}}, []string{"path"}, true},
		{"list_sections", "List the sections of a handbook page.", []Property{{"path", "Path of the page."}}, []string{"path"}, true},
		{"list_recent_changes", "List the pages changed within a window.", []Property{{propWindow, "Window to look back, for example 168h."}}, nil, true},
		{"propose_change", "Open a change proposal for a page with the new text.", []Property{{"path", "Path of the page."}, {"text", "The proposed text."}, {"summary", "One-line summary of the change."}}, []string{"path", "text"}, false},
	}},
	{"tickets", []toolShape{
		{"list_tickets", "List the tickets of a queue with their state and age.", []Property{{"queue", "Queue name."}, {"state", "Restrict to open, waiting or closed tickets."}}, nil, true},
		{"read_ticket", "Return one ticket with its comments.", []Property{{propTicket, descTicket}}, []string{propTicket}, true},
		{"search_tickets", "Search tickets by free text.", []Property{{propQuery, descQuery}, {"limit", "Maximum number of tickets."}}, []string{propQuery}, true},
		{"comment_ticket", "Add a comment to a ticket.", []Property{{propTicket, descTicket}, {"text", "Comment text."}}, []string{propTicket, "text"}, false},
		{"assign_ticket", "Assign a ticket to a person.", []Property{{propTicket, descTicket}, {"assignee", "Person to assign."}}, []string{propTicket, "assignee"}, false},
		{"close_ticket", "Close a ticket with a resolution.", []Property{{propTicket, descTicket}, {"resolution", "Resolution text."}}, []string{propTicket, "resolution"}, false},
	}},
	{"runbook-library", []toolShape{
		{"list_runbooks", "List the runbooks with their title and the alerts they cover.", []Property{{"alert", "Restrict to runbooks covering this alert."}}, nil, true},
		{"read_runbook", "Return one runbook as text.", []Property{{"runbook", "Runbook identifier."}}, []string{"runbook"}, true},
		{"search_runbooks", "Search runbooks by free text.", []Property{{propQuery, descQuery}}, []string{propQuery}, true},
		{"list_recent_runs", "List the recent executions of a runbook.", []Property{{"runbook", "Runbook identifier."}, {propWindow, "Window to look back."}}, []string{"runbook"}, true},
	}},
}

// Workflow names are verb-object-qualifier combinations; the generator takes
// the first workflowCount distinct combinations in a seeded order.
var (
	workflowVerbs      = []string{"check", "inspect", "compare", "summarize", "triage", "reconcile", "snapshot", "escalate", "drain", "rotate", "audit", "forecast"}
	workflowObjects    = []string{"node-pool-capacity", "release-drift", "alert-noise", "log-volume", "certificate-expiry", "quota-headroom", "upgrade-readiness", "silence-hygiene", "scrape-health", "ingress-errors", "usage-anomalies", "retention-gaps"}
	workflowQualifiers = []string{"per-installation", "across-organizations", "before-upgrade", "after-incident", "for-the-week", "on-request"}
)
