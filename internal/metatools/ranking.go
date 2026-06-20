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
)

// rankedDoc pairs a corpus document's index with its relevance score.
type rankedDoc struct {
	index int
	score float64
}

// rankBM25 scores docs against the query and returns their indices ordered by
// descending relevance. Documents with a zero score (no query term matched)
// are dropped, so a query narrows the candidate set rather than reordering the
// whole catalogue.
//
// This is a self-contained lexical ranker: tokenisation is lowercase
// alphanumeric runs (snake_case and kebab-case names split naturally; camelCase
// boundaries are also split), IDF is computed over the candidate corpus, and
// no embedding index or external dependency is involved. Embeddings can replace
// this later if lexical recall proves insufficient (see muster#868).
func rankBM25(query string, docs []string) []rankedDoc {
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 || len(docs) == 0 {
		return nil
	}
	// The set of distinct query terms is invariant across documents, so compute
	// it once and reuse it for both the IDF precompute and per-document scoring.
	queryTermSet := uniqueTerms(queryTerms)

	docTerms := make([][]string, len(docs))
	docFreq := make(map[string]int) // documents containing a term
	var totalLen int
	for i, doc := range docs {
		terms := tokenize(doc)
		docTerms[i] = terms
		totalLen += len(terms)
		for term := range uniqueTerms(terms) {
			docFreq[term]++
		}
	}

	avgLen := 0.0
	if len(docs) > 0 {
		avgLen = float64(totalLen) / float64(len(docs))
	}
	n := float64(len(docs))

	// Precompute IDF per query term.
	idf := make(map[string]float64, len(queryTermSet))
	for term := range queryTermSet {
		df := float64(docFreq[term])
		// Okapi BM25 IDF with +1 to keep it non-negative for common terms.
		idf[term] = math.Log(1 + (n-df+0.5)/(df+0.5))
	}

	ranked := make([]rankedDoc, 0, len(docs))
	for i, terms := range docTerms {
		tf := make(map[string]int, len(terms))
		for _, t := range terms {
			tf[t]++
		}
		dl := float64(len(terms))
		var score float64
		for term := range queryTermSet {
			f := float64(tf[term])
			if f == 0 {
				continue
			}
			denom := f + bm25K1*(1-bm25B+bm25B*dl/avgLen)
			score += idf[term] * (f * (bm25K1 + 1)) / denom
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
