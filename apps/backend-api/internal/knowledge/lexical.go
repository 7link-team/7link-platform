package knowledge

import (
	"golang.org/x/text/unicode/norm"
	"math"
	"strings"
	"unicode"
)

type lexicalCorpus struct {
	documents     int
	averageLength float64
	frequency     map[string]int
}

func lexicalTerms(text string) []string {
	return lexicalTokenize(text, false)
}
func lexicalTokenize(text string, _ bool) []string {
	var out []string
	var word []rune
	cjkWord := false
	flush := func() {
		if len(word) == 0 {
			return
		}
		if !cjkWord {
			word = word[:min(64, len(word))]
			if len(word) > 1 {
				out = append(out, strings.ToLower(string(word)))
			}
			start := 0
			boundaries := []int{}
			for i := 1; i < len(word); i++ {
				if unicode.IsLower(word[i-1]) && unicode.IsUpper(word[i]) || unicode.IsLetter(word[i-1]) && unicode.IsNumber(word[i]) || unicode.IsNumber(word[i-1]) && unicode.IsLetter(word[i]) {
					boundaries = append(boundaries, i)
				}
			}
			if len(boundaries) > 0 {
				boundaries = append(boundaries, len(word))
				for _, end := range boundaries {
					if end-start >= 2 {
						out = append(out, strings.ToLower(string(word[start:end])))
					}
					start = end
				}
			}
		} else {
			if len(word) == 1 {
				out = append(out, string(word))
			}
			for i := 0; i+1 < len(word); i++ {
				out = append(out, string(word[i:i+2]))
			}
		}
		word = nil
	}
	for _, r := range norm.NFKC.String(text) {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			flush()
			continue
		}
		cjk := unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
		if len(word) > 0 && cjk != cjkWord {
			flush()
		}
		cjkWord = cjk
		word = append(word, r)
	}
	flush()
	return out
}
func newLexicalCorpus(documents []string) lexicalCorpus {
	c := lexicalCorpus{documents: len(documents), frequency: map[string]int{}}
	for _, document := range documents {
		terms := lexicalTerms(document)
		c.averageLength += float64(len(terms))
		seen := map[string]bool{}
		for _, term := range terms {
			if !seen[term] {
				c.frequency[term]++
				seen[term] = true
			}
			if runes := []rune(term); len(runes) == 2 && (unicode.Is(unicode.Han, runes[0]) || unicode.Is(unicode.Hiragana, runes[0]) || unicode.Is(unicode.Katakana, runes[0]) || unicode.Is(unicode.Hangul, runes[0])) {
				prefix := string(runes[0])
				if !seen[prefix] {
					c.frequency[prefix]++
					seen[prefix] = true
				}
			}
		}
	}
	if c.documents > 0 {
		c.averageLength /= float64(c.documents)
	}
	return c
}

// BM25 bounds repetition and normalizes document length. All three dialects
// share these semantics; this does not misrepresent optional SQLite FTS5 as an
// installed capability or depend on PostgreSQL/MySQL-only query syntax.
func (c lexicalCorpus) score(document string, tokens []string) (score float64, raw, hits int) {
	terms := lexicalTerms(document)
	counts := map[string]int{}
	for _, term := range terms {
		counts[term]++
	}
	if c.documents == 0 || c.averageLength == 0 {
		return
	}
	seen := map[string]bool{}
	for _, token := range tokens {
		if seen[token] {
			continue
		}
		seen[token] = true
		n := counts[token]
		// A single CJK query is a prefix query, matching its leading bigrams.
		if runes := []rune(token); len(runes) == 1 && (unicode.Is(unicode.Han, runes[0]) || unicode.Is(unicode.Hiragana, runes[0]) || unicode.Is(unicode.Katakana, runes[0]) || unicode.Is(unicode.Hangul, runes[0])) {
			n = 0
			for term, count := range counts {
				if strings.HasPrefix(term, token) {
					n += count
				}
			}
		}
		if n == 0 {
			continue
		}
		raw += n
		hits++
		idf := math.Log(1 + (float64(c.documents-c.frequency[token])+.5)/(float64(c.frequency[token])+.5))
		tf := float64(n)
		score += idf * tf * 2.2 / (tf + 1.2*(.25+.75*float64(len(terms))/c.averageLength))
	}
	return
}
