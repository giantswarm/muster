package scale

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
)

var (
	fixtureOnce sync.Once
	fixture     *Fixture
)

// Get returns the fixture, generated once per process. The generator is
// deterministic: the same shape and seed give the same fixture, and the
// committed YAML is its rendering (TestFixtureFilesAreCurrent proves it).
func Get() *Fixture {
	fixtureOnce.Do(func() { fixture = generate() })
	return fixture
}

// generate builds the fixture from the shape tables and the seed.
func generate() *Fixture {
	f := &Fixture{MusterOAuthServer: musterOAuthServer, FleetIssuer: fleetIssuer}
	rng := rand.New(rand.NewPCG(seed, seed)) //nolint:gosec // a fixture generator, not a secret

	for _, fam := range families {
		f.Documents = append(f.Documents, familyDocuments(fam)...)
		for i := 0; i < fam.members; i++ {
			installation := installations[i%maxInstallations]
			f.Servers = append(f.Servers, Server{
				Name:         installation + "-" + fam.name,
				Installation: installation,
				Family:       fam.name,
				InstanceArg:  instanceArg,
				Document:     documentName(fam.name, i%fam.variants),
				SessionAuth:  true,
			})
		}
	}
	for _, in := range inHouseServers {
		f.Documents = append(f.Documents, Document{Name: in.name, Tools: tools(in.tools)})
		f.Servers = append(f.Servers, Server{Name: in.name, Document: in.name})
	}

	f.Workflows = generateWorkflows(rng, f)

	for i := 0; i < sessionCount; i++ {
		f.Sessions = append(f.Sessions, Session{
			ID:      fmt.Sprintf("sess-%04d", i+1),
			Subject: fmt.Sprintf("person-%03d@people.fixture.invalid", i%personCount+1),
		})
	}
	return f
}

// familyDocuments renders a family's variants: variant k offers the first
// len(tools)-(variants-1-k) tools of the catalogue, so every variant is a
// distinct document and the newest one is the whole catalogue.
func familyDocuments(fam familyShape) []Document {
	docs := make([]Document, 0, fam.variants)
	for k := 0; k < fam.variants; k++ {
		n := len(fam.tools) - (fam.variants - 1 - k)
		docs = append(docs, Document{Name: documentName(fam.name, k), Tools: tools(fam.tools[:n])})
	}
	return docs
}

func documentName(family string, variant int) string {
	return fmt.Sprintf("%s-v%d", family, variant+1)
}

func tools(shapes []toolShape) []Tool {
	out := make([]Tool, 0, len(shapes))
	for _, t := range shapes {
		out = append(out, Tool{
			Name:        t.name,
			Description: t.description,
			Properties:  append([]Property(nil), t.props...),
			Required:    append([]string(nil), t.required...),
			ReadOnly:    t.readOnly,
		})
	}
	return out
}

// generateWorkflows produces workflowCount workflows named by distinct
// verb-object-qualifier combinations, each with one to five steps over the
// read-only family tools (with the instance argument templated from the
// workflow's input) and now and then an in-house tool.
func generateWorkflows(rng *rand.Rand, f *Fixture) []Workflow {
	names := workflowNames(rng)
	readOnly := readOnlyFamilyTools(f)
	inHouse := readOnlyInHouseTools(f)

	workflows := make([]Workflow, 0, len(names))
	for i, name := range names {
		verb, object, qualifier := splitName(name)
		wf := Workflow{
			Name:        name,
			Description: fmt.Sprintf("Runbook: %s %s %s.", strings.ToUpper(verb[:1])+verb[1:], strings.ReplaceAll(object, "-", " "), strings.ReplaceAll(qualifier, "-", " ")),
			Labels:      map[string]string{"fixture.invalid/verb": verb},
			Args: []Arg{
				{Name: instanceArg, Type: "string", Description: "The installation to run against.", Required: true},
				{Name: propWindow, Type: "string", Description: "How far to look back, for example 24h.", Required: false},
			},
		}
		steps := 1 + rng.IntN(5)
		for s := 0; s < steps; s++ {
			var step Step
			if s > 0 && rng.IntN(4) == 0 {
				pick := inHouse[rng.IntN(len(inHouse))]
				step = Step{ID: fmt.Sprintf("step-%d", s+1), Tool: pick.exposed, Args: []StepArg{{pick.firstArg, fmt.Sprintf("%s %s", verb, strings.ReplaceAll(object, "-", " "))}}}
			} else {
				pick := readOnly[rng.IntN(len(readOnly))]
				step = Step{ID: fmt.Sprintf("step-%d", s+1), Tool: pick.exposed, Args: []StepArg{{instanceArg, "{{ .input." + instanceArg + " }}"}}}
				if pick.firstArg != "" {
					step.Args = append(step.Args, StepArg{pick.firstArg, "{{ .input.window }}"})
				}
			}
			wf.Steps = append(wf.Steps, step)
		}
		if i%3 == 0 {
			wf.Labels["fixture.invalid/tier"] = "gold"
		}
		workflows = append(workflows, wf)
	}
	return workflows
}

// workflowNames returns workflowCount distinct verb-object-qualifier names in
// a seeded order.
func workflowNames(rng *rand.Rand) []string {
	all := make([]string, 0, len(workflowVerbs)*len(workflowObjects)*len(workflowQualifiers))
	for _, v := range workflowVerbs {
		for _, o := range workflowObjects {
			for _, q := range workflowQualifiers {
				all = append(all, v+"-"+o+"-"+q)
			}
		}
	}
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	names := all[:workflowCount]
	sort.Strings(names)
	return names
}

func splitName(name string) (verb, object, qualifier string) {
	for _, v := range workflowVerbs {
		if rest, ok := strings.CutPrefix(name, v+"-"); ok {
			for _, q := range workflowQualifiers {
				if obj, ok := strings.CutSuffix(rest, "-"+q); ok {
					return v, obj, q
				}
			}
		}
	}
	return name, "", ""
}

// toolPick is an exposed tool name with the first non-instance argument a
// workflow step passes it.
type toolPick struct{ exposed, firstArg string }

// readOnlyFamilyTools lists every read-only family tool once (the family
// exposes one name for all its members), sorted.
func readOnlyFamilyTools(f *Fixture) []toolPick {
	seen := map[string]toolPick{}
	for _, s := range f.Servers {
		if s.Family == "" {
			continue
		}
		doc, _ := f.DocumentByName(s.Document)
		for _, t := range doc.Tools {
			if !t.ReadOnly {
				continue
			}
			name := exposedToolName(s, t.Name)
			if _, dup := seen[name]; dup {
				continue
			}
			pick := toolPick{exposed: name}
			for _, p := range t.Properties {
				if p.Name == propWindow {
					pick.firstArg = p.Name
				}
			}
			seen[name] = pick
		}
	}
	return sortedPicks(seen)
}

// readOnlyInHouseTools lists the read-only tools of the in-house servers.
func readOnlyInHouseTools(f *Fixture) []toolPick {
	seen := map[string]toolPick{}
	for _, s := range f.Servers {
		if s.Family != "" {
			continue
		}
		doc, _ := f.DocumentByName(s.Document)
		for _, t := range doc.Tools {
			if !t.ReadOnly || len(t.Properties) == 0 {
				continue
			}
			seen[exposedToolName(s, t.Name)] = toolPick{exposed: exposedToolName(s, t.Name), firstArg: t.Properties[0].Name}
		}
	}
	return sortedPicks(seen)
}

func sortedPicks(m map[string]toolPick) []toolPick {
	out := make([]toolPick, 0, len(m))
	for _, p := range m {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].exposed < out[j].exposed })
	return out
}
