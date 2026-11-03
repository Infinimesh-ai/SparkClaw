package docindex

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Terms tokenizes NFKC-normalized text for keyword matching. Han characters become
// overlapping bigrams (a lone character stays a unigram), which matches
// Chinese words without a dictionary; letters and digits become lowercase
// words. Punctuation and whitespace separate terms.
func Terms(text string) []string {
	terms := []string{}
	var word strings.Builder
	han := []rune{}
	flushWord := func() {
		if word.Len() > 0 {
			terms = append(terms, word.String())
			word.Reset()
		}
	}
	flushHan := func() {
		switch len(han) {
		case 0:
		case 1:
			terms = append(terms, string(han))
		default:
			for index := 0; index+1 < len(han); index++ {
				terms = append(terms, string(han[index:index+2]))
			}
		}
		han = han[:0]
	}
	for _, r := range norm.NFKC.String(text) {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushHan()
			word.WriteRune(unicode.ToLower(r))
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return terms
}

// queryTerms tokenizes every keyword and removes duplicates, keeping order.
func queryTerms(keywords []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, keyword := range keywords {
		for _, term := range Terms(keyword) {
			if !seen[term] {
				seen[term] = true
				out = append(out, term)
			}
		}
	}
	return out
}
