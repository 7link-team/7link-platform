package knowledge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLexicalBM25WholeTermsLengthNormalizationAndRareTerms(t *testing.T) {
	corpus := newLexicalCorpus([]string{"export amount", "export " + strings.Repeat("padding ", 100), "export common"})
	short, raw, hits := corpus.score("export amount", []string{"export", "amount"})
	long, _, _ := corpus.score("export "+strings.Repeat("padding ", 100), []string{"export", "amount"})
	require.Greater(t, short, long)
	require.Equal(t, 2, raw)
	require.Equal(t, 2, hits)
	_, raw, hits = corpus.score("exporter amounts", []string{"export", "amount"})
	require.Zero(t, raw)
	require.Zero(t, hits)
	rare, _, _ := corpus.score("export amount", []string{"amount"})
	common, _, _ := corpus.score("export amount", []string{"export"})
	require.Greater(t, rare, common)
	zero, _, _ := newLexicalCorpus(nil).score("anything", []string{"anything"})
	require.Zero(t, zero)
}

func TestLexicalCJKSubwordQueriesAndRepeatedTokens(t *testing.T) {
	document := "平台权限管理与安全策略"
	corpus := newLexicalCorpus([]string{document, "无关文本"})
	for _, query := range []string{"权限", "权限管理", "权"} {
		tokens := retrievalTokens(query)
		score, _, hits := corpus.score(document, tokens)
		require.Positive(t, score, query)
		require.Equal(t, len(tokens), hits, query)
	}
	require.Equal(t, []string{"权限"}, retrievalTokens("权限 权限"))
	require.Equal(t, []string{"权限", "限管", "管理"}, retrievalTokens("权限管理 权限管理"))
	single, raw, hits := corpus.score(document, []string{"权限"})
	repeated, repeatedRaw, repeatedHits := corpus.score(document, []string{"权限", "权限"})
	require.InDelta(t, single, repeated, 0)
	require.Equal(t, raw, repeatedRaw)
	require.Equal(t, hits, repeatedHits)
}
func TestLexicalCanonicalNormalizationCamelDigitsAndSingleCJKPrefix(t *testing.T) {
	require.Equal(t, []string{"getusername", "get", "user", "name", "user42", "user", "42", "ab"}, lexicalTerms("getUserName user42 ＡＢ a 1"))
	require.Equal(t, []string{"数据", "据库"}, lexicalTerms("数据库"))
	require.Equal(t, []string{strings.Repeat("x", 64)}, lexicalTerms(strings.Repeat("x", 80)))
	require.Len(t, retrievalModeTokens(strings.Repeat("word ", 30)+strings.Join([]string{"one", "two", "three"}, " "), "ALL"), 4)
	corpus := newLexicalCorpus([]string{"数据库 库存"})
	score, _, hits := corpus.score("数据库", retrievalTokens("库"))
	require.Zero(t, score)
	require.Zero(t, hits)
	score, _, hits = corpus.score("库存", retrievalTokens("库"))
	require.Positive(t, score)
	require.Equal(t, 1, hits)
}
