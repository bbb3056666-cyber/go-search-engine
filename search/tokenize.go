package search

import (
	"fmt"
	"github.com/go-ego/gse"
	"strings"
	"unicode"
)

var segmenter = gse.Segmenter{SkipLog: true, SkipSubSeg: true}

func init() {
	// 启动时加载一次中文词典，建索引和查询共用
	if err := segmenter.LoadDictEmbed("zh_s"); err != nil {
		panic(fmt.Errorf("加载分词词典：%w", err))
	}
}

func tokenize(text string) []string {
	// 统一转成小写，再调用 gse 精准分词
	words := segmenter.Cut(strings.ToLower(text), true)
	tokens := make([]string, 0, len(words))

	// 去掉空白和纯标点，保留包含字母、汉字或数字的词元
	for _, word := range words {
		word = strings.TrimSpace(word)
		if strings.IndexFunc(word, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			tokens = append(tokens, word)
		}
	}
	return tokens
}
