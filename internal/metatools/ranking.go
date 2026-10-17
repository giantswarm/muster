package metatools

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// BM25 ranking parameters. The defaults (k1=1.5, b=0.75) are the textbook
// Okapi BM25 values and are fine for the small, descriptive corpus muster
// ranks (a few hundred tool names and descriptions).
const (
	bm25K1 = 1.5
	bm25B  = 0.75

	// Field weights for the BM25F extension. A query term that matches a tool's
	// name is far more indicative of intent than one that merely appears in the
	// prose description, so name matches are weighted higher. Without this, a
	// discriminating term (e.g. "pods") buried in a description loses to a
	// ubiquitous verb (e.g. "list") that happens to head many tool names.
	// A description's summary is its first line that is not blank; the lines
	// below it are its details, which still match but weigh less.
	bm25NameWeight    = 3.0
	bm25SummaryWeight = 1.0
	bm25DetailsWeight = 0.4

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

// stopWords are English function words. Descriptions are full of them ("the",
// "of", "to"), so a natural-language query ("how do I restart the service")
// would match nearly every tool through them; they are dropped from the query
// unless it has no other word. Negations ("not", "no") are kept.
var stopWords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "but": {}, "if": {}, "then": {}, "than": {}, "so": {},
	"of": {}, "to": {}, "in": {}, "on": {}, "at": {}, "by": {}, "for": {}, "from": {}, "with": {}, "into": {},
	"about": {}, "as": {}, "is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"has": {}, "have": {}, "had": {},
	"it": {}, "its": {}, "this": {}, "that": {}, "these": {}, "those": {}, "there": {}, "here": {},
	"do": {}, "does": {}, "did": {}, "can": {}, "could": {}, "should": {}, "would": {}, "will": {}, "may": {}, "might": {},
	"i": {}, "me": {}, "my": {}, "you": {}, "your": {}, "we": {}, "our": {}, "they": {}, "their": {},
	"how": {}, "what": {}, "which": {}, "who": {}, "when": {}, "where": {}, "why": {}, "please": {},
}

// rankedDoc pairs a corpus document's index with its relevance score.
type rankedDoc struct {
	index int
	score float64
}

// rankDoc is a tool projected into the three fields the ranker weighs
// separately: the tool name, the summary of its description and the rest of
// the description, its details (see splitDescription).
type rankDoc struct {
	name    string
	summary string
	details string
}

// numRankFields is the number of fields of a rankDoc.
const numRankFields = 3

// rankFieldWeights are the BM25F weights of the fields, in the order fields
// returns them.
var rankFieldWeights = [numRankFields]float64{bm25NameWeight, bm25SummaryWeight, bm25DetailsWeight}

// fields returns the text of the doc's fields, in the order of rankFieldWeights.
func (d rankDoc) fields() [numRankFields]string {
	return [numRankFields]string{d.name, d.summary, d.details}
}

// rankBM25 scores docs against the query and returns their indices ordered by
// descending relevance. Documents with a zero score (no query term matched)
// are dropped, so a query narrows the candidate set rather than reordering the
// whole catalogue.
//
// This is a self-contained lexical ranker using a BM25F-style field-weighted
// extension: a term's frequency is normalised per field (name, summary, details)
// against that field's average length over the documents that have the field,
// then combined with rankFieldWeights, so a match weighs most in the name and
// least in the details. Ubiquitous CRUD verbs are down-weighted (see
// stopVerbs) so the discriminating noun drives ranking, and function words are
// dropped from the query (see stopWords).
// Tokenisation is lowercase alphanumeric runs (snake_case and kebab-case names
// split naturally; camelCase boundaries are also split), each folded so that a
// plural matches its singular (see foldPlural), IDF is computed over
// the candidate corpus, and no embedding index or external dependency is
// involved. Embeddings can replace this later if lexical recall proves
// insufficient (see muster#868).
func rankBM25(query string, docs []rankDoc) []rankedDoc {
	terms := queryTerms(query)
	if len(terms) == 0 || len(docs) == 0 {
		return nil
	}
	isQueryTerm := make(map[string]bool, len(terms))
	for _, t := range terms {
		isQueryTerm[t] = true
	}

	// Per document and field, count only the query terms; the field length
	// counts every token. A field's average length is taken over the documents
	// that have the field, so the many tools with a one-line description do not
	// shrink the average details length and crush every details match.
	fieldTf := make([][numRankFields]map[string]int, len(docs))
	fieldLen := make([][numRankFields]int, len(docs))
	docFreq := make(map[string]int, len(terms)) // documents containing a query term (in any field)
	var totalLen, withField [numRankFields]int
	for i, doc := range docs {
		seen := make(map[string]bool, len(terms))
		for f, text := range doc.fields() {
			tokens := rankTerms(text)
			tf := make(map[string]int, len(terms))
			for _, t := range tokens {
				if isQueryTerm[t] {
					tf[t]++
					seen[t] = true
				}
			}
			fieldTf[i][f] = tf
			fieldLen[i][f] = len(tokens)
			if len(tokens) > 0 {
				totalLen[f] += len(tokens)
				withField[f]++
			}
		}
		for t := range seen {
			docFreq[t]++
		}
	}
	var avgFieldLen [numRankFields]float64
	for f := range avgFieldLen {
		avgFieldLen[f] = avgLen(totalLen[f], withField[f])
	}

	n := float64(len(docs))
	idf := make(map[string]float64, len(terms))
	for _, term := range terms {
		df := float64(docFreq[term])
		// Okapi BM25 IDF with +1 to keep it non-negative for common terms.
		idf[term] = math.Log(1 + (n-df+0.5)/(df+0.5))
	}

	ranked := make([]rankedDoc, 0, len(docs))
	for i := range docs {
		var score float64
		// terms is sorted, so equally relevant documents sum the same
		// contributions in the same order and get the same score.
		for _, term := range terms {
			// BM25F: normalise each field's raw frequency by its own length,
			// then combine the weighted, length-normalised frequencies before
			// the saturating BM25 term.
			var wtf float64
			for f, weight := range rankFieldWeights {
				wtf += weight * normTf(float64(fieldTf[i][f][term]), float64(fieldLen[i][f]), avgFieldLen[f])
			}
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

// queryTerms returns the distinct terms of the query, folded (see foldPlural)
// and sorted, without its stopWords; a query of only stop words keeps them.
func queryTerms(query string) []string {
	var all, content []string
	inAll, inContent := make(map[string]bool), make(map[string]bool)
	for _, t := range tokenize(query) {
		folded := foldPlural(t)
		if !inAll[folded] {
			inAll[folded] = true
			all = append(all, folded)
		}
		if _, isStopWord := stopWords[t]; !isStopWord && !inContent[folded] {
			inContent[folded] = true
			content = append(content, folded)
		}
	}
	if len(content) == 0 {
		content = all
	}
	sort.Strings(content)
	return content
}

// rankTerms tokenises s and folds each token (see foldPlural): the terms the
// ranker compares, for the query and for every document field alike.
func rankTerms(s string) []string {
	terms := tokenize(s)
	for i, t := range terms {
		terms[i] = foldPlural(t)
	}
	return terms
}

// singularsEndingInS are singular words and acronyms that end in "s".
// foldPlural keeps them rather than dropping the "s" as if they were plurals,
// which would leave "news" meeting "new", "https" meeting "http" and "kms"
// meeting "km", and "status" never meeting "statuses"; it folds their "es"
// plural to them ("aliases" -> "alias").
var singularsEndingInS = map[string]struct{}{
	"alias": {}, "analysis": {}, "atlas": {}, "axis": {}, "basis": {}, "bias": {}, "bonus": {}, "bus": {},
	"campus": {}, "canvas": {}, "census": {}, "consensus": {}, "corpus": {}, "dns": {}, "focus": {}, "gas": {},
	"gcs": {}, "https": {}, "lens": {}, "news": {}, "nexus": {}, "plus": {}, "prometheus": {}, "radius": {},
	"redis": {}, "series": {}, "species": {}, "status": {}, "tls": {}, "virus": {},
	// AWS services, which would otherwise become two-letter keys.
	"aws": {}, "ecs": {}, "efs": {}, "eks": {}, "kms": {}, "rds": {}, "sns": {}, "sqs": {},
}

// irregularPlurals maps plurals that no suffix rule folds to their singular.
var irregularPlurals = map[string]string{
	"analyses": "analysis",
	"children": "child",
	"indices":  "index",
	"matrices": "matrix",
	"vertices": "vertex",
}

// foldPlural maps a lowercase token to a key it shares with its singular or
// plural form, so that "policies" matches "policy" and "versions" matches
// "version". The key is not always a word ("policy" and "policies" both become
// "polici"); it only has to be the same for both forms, and the query is folded
// the same way as the documents.
//
// The rules, in order:
//   - a stop word ("has", "this"), a token with a digit ("k8s", "s3"), one
//     ending in "ss" ("access") or one listed in singularsEndingInS ("status",
//     "https") is kept, and the "es" plural of a listed word becomes that word
//     ("statuses" -> "status");
//   - an irregular plural becomes its singular ("indices" -> "index");
//   - "ies" becomes "i" ("policies" -> "polici") when two letters stay before it;
//   - otherwise a final "s" is dropped when two letters stay ("pods" -> "pod",
//     "apis" -> "api", "vms" -> "vm");
//   - then a final "e" is dropped when three letters stay, unless they end in
//     consonant-vowel-consonant (Porter's rule), so that "caches" -> "cache"
//     -> "cach" meets "cache" and "matches" -> "matche" -> "match" meets
//     "match", while "plane" stays apart from "plan" and "state" from "stat";
//   - or a final "y" after a consonant becomes "i", so that "policy" meets
//     "policies" ("key" and "day" keep theirs, as their plurals do).
//
// It is a light, predictable fold rather than a full stemmer: it does not
// conflate verb forms ("restarting", "restarted"), and so does not conflate
// unrelated words that share a stem ("policy" and "police"). A singular in "s"
// that is not listed loses its "s" ("lens" would, without the list), and a
// word that differs from another only by a final "e" after a stem that does not
// end consonant-vowel-consonant meets it ("range" and "rang").
func foldPlural(t string) string {
	if isKeptAsIs(t) {
		return t
	}
	if singular, ok := irregularPlurals[t]; ok {
		return singular
	}
	if stem, ok := strings.CutSuffix(t, "es"); ok {
		if _, ok := singularsEndingInS[stem]; ok {
			return stem
		}
	}
	// Every suffix handled below is ASCII, so the token is sliced as bytes.
	switch {
	case len(t) >= 5 && strings.HasSuffix(t, "ies"):
		t = t[:len(t)-3] + "i"
	case len(t) >= 3 && t[len(t)-1] == 's':
		t = t[:len(t)-1]
	}
	switch n := len(t); {
	case n >= 4 && t[n-1] == 'e' && !endsCVC(t[:n-1]):
		t = t[:n-1]
	case n >= 3 && t[n-1] == 'y' && !isVowelAt(t, n-2):
		t = t[:n-1] + "i"
	}
	return t
}

// isKeptAsIs reports whether foldPlural keeps t: a stop word, a token with a
// digit, or a singular ending in "s".
func isKeptAsIs(t string) bool {
	if _, ok := stopWords[t]; ok {
		return true
	}
	if _, ok := singularsEndingInS[t]; ok {
		return true
	}
	return strings.HasSuffix(t, "ss") || strings.ContainsFunc(t, unicode.IsDigit)
}

// isVowelAt reports whether the byte at i is a vowel: a, e, i, o, u, or a "y"
// after a consonant ("type", not "key").
func isVowelAt(w string, i int) bool {
	switch w[i] {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	case 'y':
		return i > 0 && !isVowelAt(w, i-1)
	}
	return false
}

// endsCVC reports whether w ends in consonant-vowel-consonant, the last one not
// w, x or y ("plan", "stat", but not "box").
func endsCVC(w string) bool {
	n := len(w)
	if n < 3 {
		return false
	}
	switch w[n-1] {
	case 'w', 'x', 'y':
		return false
	}
	return !isVowelAt(w, n-3) && isVowelAt(w, n-2) && !isVowelAt(w, n-1)
}
