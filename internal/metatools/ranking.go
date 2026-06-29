package metatools

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// BM25 ranking parameters. The defaults (k1=1.5, b=0.75) are the textbook
// Okapi BM25 values and are fine for the small, descriptive corpus muster
// ranks (a few hundred tool names + one-line summaries).
const (
	bm25K1 = 1.5
	bm25B  = 0.75

	// Field weights for the BM25F extension. A query term that matches a tool's
	// name is far more indicative of intent than one that merely appears in the
	// prose description, so name matches are weighted higher. Without this, a
	// discriminating term (e.g. "pods") buried in a description loses to a
	// ubiquitous verb (e.g. "list") that happens to head many tool names.
	bm25NameWeight = 3.0
	bm25DescWeight = 1.0

	// stopVerbWeight down-weights the contribution of ubiquitous CRUD verbs so
	// that the discriminating noun in a query drives ranking. "list pods" should
	// surface pod tools, not every *_list_* tool in the catalogue.
	stopVerbWeight = 0.25
)

// stopVerbs are the generic action verbs that head a large fraction of tool
// names ("x_pd_list_*", "core_*_get", …). They carry almost no intent on their
// own, so their score contribution is scaled by stopVerbWeight. They are not
// dropped: a query of only "list" still ranks list-shaped tools (every doc is
// scaled equally, preserving relative order).
var stopVerbs = map[string]struct{}{
	"list": {},
	"get":  {},
}

// rankedDoc pairs a corpus document's index with its relevance score.
type rankedDoc struct {
	index int
	score float64
}

// rankDoc is a tool projected into the two fields the ranker weighs separately:
// the tool name and its (summarised) description.
type rankDoc struct {
	name        string
	description string
}

// rankBM25 scores docs against the query and returns their indices ordered by
// descending relevance. Documents with a zero score (no query term matched)
// are dropped, so a query narrows the candidate set rather than reordering the
// whole catalogue.
//
// This is a self-contained lexical ranker using a BM25F-style field-weighted
// extension: a term's frequency is normalised per field (name vs description)
// against that field's average length, then combined with bm25NameWeight /
// bm25DescWeight so name matches dominate. Ubiquitous CRUD verbs are
// down-weighted (see stopVerbs) so the discriminating noun drives ranking.
// Tokenisation is lowercase alphanumeric runs (snake_case and kebab-case names
// split naturally; camelCase boundaries are also split), IDF is computed over
// the candidate corpus, and no embedding index or external dependency is
// involved. Embeddings can replace this later if lexical recall proves
// insufficient (see muster#868).
func rankBM25(query string, docs []rankDoc) []rankedDoc {
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 || len(docs) == 0 {
		return nil
	}
	// The set of distinct query terms is invariant across documents, so compute
	// it once and reuse it for both the IDF precompute and per-document scoring.
	queryTermSet := uniqueTerms(queryTerms)

	nameTerms := make([][]string, len(docs))
	descTerms := make([][]string, len(docs))
	docFreq := make(map[string]int) // documents containing a term (in either field)
	var totalNameLen, totalDescLen int
	for i, doc := range docs {
		nt := tokenize(doc.name)
		dt := tokenize(doc.description)
		nameTerms[i] = nt
		descTerms[i] = dt
		totalNameLen += len(nt)
		totalDescLen += len(dt)
		seen := make(map[string]struct{}, len(nt)+len(dt))
		for _, t := range nt {
			seen[t] = struct{}{}
		}
		for _, t := range dt {
			seen[t] = struct{}{}
		}
		for term := range seen {
			docFreq[term]++
		}
	}

	n := float64(len(docs))
	avgNameLen := avgLen(totalNameLen, len(docs))
	avgDescLen := avgLen(totalDescLen, len(docs))

	// Precompute IDF per query term over the combined corpus.
	idf := make(map[string]float64, len(queryTermSet))
	for term := range queryTermSet {
		df := float64(docFreq[term])
		// Okapi BM25 IDF with +1 to keep it non-negative for common terms.
		idf[term] = math.Log(1 + (n-df+0.5)/(df+0.5))
	}

	ranked := make([]rankedDoc, 0, len(docs))
	for i := range docs {
		nameTf := termFreq(nameTerms[i])
		descTf := termFreq(descTerms[i])
		nameLen := float64(len(nameTerms[i]))
		descLen := float64(len(descTerms[i]))
		var score float64
		for term := range queryTermSet {
			// BM25F: normalise each field's raw frequency by its own length,
			// then combine the weighted, length-normalised frequencies before
			// the saturating BM25 term.
			wtf := bm25NameWeight*normTf(float64(nameTf[term]), nameLen, avgNameLen) +
				bm25DescWeight*normTf(float64(descTf[term]), descLen, avgDescLen)
			if wtf == 0 {
				continue
			}
			contribution := idf[term] * wtf * (bm25K1 + 1) / (wtf + bm25K1)
			if _, isStopVerb := stopVerbs[term]; isStopVerb {
				contribution *= stopVerbWeight
			}
			score += contribution
		}
		if score > 0 {
			ranked = append(ranked, rankedDoc{index: i, score: score})
		}
	}

	sort.SliceStable(ranked, func(a, b int) bool {
		return ranked[a].score > ranked[b].score
	})
	return ranked
}

// normTf length-normalises a raw term frequency against the field's average
// length, the BM25 way (b controls how aggressively long fields are penalised).
func normTf(f, fieldLen, avgFieldLen float64) float64 {
	if f == 0 || avgFieldLen == 0 {
		return 0
	}
	return f / (1 - bm25B + bm25B*fieldLen/avgFieldLen)
}

// avgLen returns the mean field length, guarding against an empty corpus.
func avgLen(total, count int) float64 {
	if count == 0 {
		return 0
	}
	return float64(total) / float64(count)
}

// termFreq counts occurrences of each term in a tokenised field.
func termFreq(terms []string) map[string]int {
	tf := make(map[string]int, len(terms))
	for _, t := range terms {
		tf[t]++
	}
	return tf
}

// tokenize lowercases the text and splits it into alphanumeric tokens, also
// breaking camelCase boundaries (e.g. "ListPods" -> "list", "pods").
func tokenize(s string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			tokens = append(tokens, b.String())
			b.Reset()
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case unicode.IsLetter(r):
			// Split lowerUpper camelCase boundaries: "aB" starts a new token.
			if i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]) {
				flush()
			}
			b.WriteRune(unicode.ToLower(r))
		case unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return tokens
}

// uniqueTerms returns the set of distinct terms in the slice.
func uniqueTerms(terms []string) map[string]struct{} {
	set := make(map[string]struct{}, len(terms))
	for _, t := range terms {
		set[t] = struct{}{}
	}
	return set
}
