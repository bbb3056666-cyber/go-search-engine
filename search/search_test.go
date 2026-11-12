package search

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func testEngine(t *testing.T, docs []Document, batchSize int) *Engine {
	t.Helper()
	path := filepath.Join(t.TempDir(), "documents.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, doc := range docs {
		if err := encoder.Encode(doc); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	e, err := build(path, batchSize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

// 独立参照：直接从原文计数，枚举每篇文章算分，再全量排序。
func reference(docs []Document, query string) Response {
	type frequencies struct {
		title, body             map[string]int
		titleLength, bodyLength int
	}
	fields := make([]frequencies, len(docs))
	titleDF, bodyDF := make(map[string]int), make(map[string]int)
	titleTotal, bodyTotal := 0, 0
	for i, doc := range docs {
		f := frequencies{title: make(map[string]int), body: make(map[string]int)}
		for _, word := range tokenize(doc.Title) {
			f.title[word]++
			f.titleLength++
		}
		for _, word := range tokenize(doc.Content) {
			f.body[word]++
			f.bodyLength++
		}
		for word := range f.title {
			titleDF[word]++
		}
		for word := range f.body {
			bodyDF[word]++
		}
		titleTotal += f.titleLength
		bodyTotal += f.bodyLength
		fields[i] = f
	}
	terms := []string{}
	seen := make(map[string]bool)
	for _, word := range tokenize(query) {
		if !seen[word] {
			terms = append(terms, word)
			seen[word] = true
		}
	}
	result := Response{Results: []Result{}}
	for i, doc := range docs {
		f := fields[i]
		score, matched := 0.0, false
		for _, term := range terms {
			if f.title[term] > 0 || f.body[term] > 0 {
				matched = true
			}
			score += 2*bm25(f.title[term], titleDF[term], len(docs), f.titleLength, float64(titleTotal)/float64(max(1, len(docs)))) + bm25(f.body[term], bodyDF[term], len(docs), f.bodyLength, float64(bodyTotal)/float64(max(1, len(docs))))
		}
		if matched {
			result.Results = append(result.Results, Result{Document: doc, Score: score})
		}
	}
	result.Total = len(result.Results)
	sort.Slice(result.Results, func(i, j int) bool {
		a, b := result.Results[i], result.Results[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Document.ID < b.Document.ID
	})
	if len(result.Results) > heapSize {
		result.Results = result.Results[:heapSize]
	}
	return result
}

func assertResults(t *testing.T, got, want Response) {
	t.Helper()
	if got.Total != want.Total || len(got.Results) != len(want.Results) {
		t.Fatalf("命中数或结果数量不同：got=%+v want=%+v", got, want)
	}
	for i := range want.Results {
		if got.Results[i].Document != want.Results[i].Document || math.Abs(got.Results[i].Score-want.Results[i].Score) > 1e-12 {
			t.Fatalf("第 %d 项不同：got=%+v want=%+v", i, got.Results[i], want.Results[i])
		}
	}
}

func TestRankingAcrossSegments(t *testing.T) {
	for _, size := range []int{0, 1, 19, 20, 21, 85} {
		docs := []Document{}
		for i := 0; i < size; i++ {
			title, body := "Go", strings.Repeat("channel ", i%9+1)
			if i%3 == 0 {
				title = "channel storage"
				body = strings.Repeat("Go ", i%5+1)
			}
			docs = append(docs, Document{ID: fmt.Sprintf("doc-%03d", i), Title: title, Content: body})
		}
		for _, batchSize := range []int{1, 7, 1000} {
			t.Run(fmt.Sprintf("size=%d/batch=%d", size, batchSize), func(t *testing.T) {
				e := testEngine(t, docs, batchSize)
				for _, query := range []string{"Go", "Go channel storage", "Go Go channel", "missingword", "，。"} {
					got, err := e.Search(query)
					if err != nil {
						t.Fatal(err)
					}
					assertResults(t, got, reference(docs, query))
				}
				added := Document{ID: "added", Title: "Go storage", Content: "storage storage Go"}
				if err := e.Add(added); err != nil {
					t.Fatal(err)
				}
				got, err := e.Search("Go storage")
				if err != nil {
					t.Fatal(err)
				}
				assertResults(t, got, reference(append(append([]Document{}, docs...), added), "Go storage"))
			})
		}
	}
}

func TestLengthsAndDF(t *testing.T) {
	e := testEngine(t, []Document{
		{ID: "1", Title: "Go Go", Content: "channel channel channel"},
		{ID: "2", Title: "channel", Content: "Go"},
	}, 1)
	if e.title.DF["go"] != 1 || e.body.DF["channel"] != 1 || e.title.TotalLength != 3 || e.body.TotalLength != 4 || e.lengths["1"] != (fieldLengths{Title: 2, Body: 3}) {
		t.Fatal("df 或文章长度统计错误")
	}
	if len(e.terms["go"]) != 2 {
		t.Fatal("同一个词应保留各批次的倒排段")
	}
	before := append([]Location{}, e.terms["go"]...)
	if err := e.Add(Document{ID: "3", Title: "Go Go", Content: "Go Go"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, e.terms["go"][:2]) || len(e.terms["go"]) != 3 {
		t.Fatal("追加应保留旧偏移量，增加新段")
	}
	if e.title.DF["go"] != 2 || e.body.DF["go"] != 2 {
		t.Fatal("新增文章 df 应按字段各计一次")
	}
}

func TestOffsetsAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "documents.jsonl")
	text := "\r\n" + `{"id":"中文1","title":"数据库","content":"中文正文"}` + "\r\n\n" + `{"id":"2","title":"Go","content":"channel"}`
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	e, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, query := range []string{"数据库", "Go"} {
		got, err := e.Search(query)
		if err != nil || got.Total != 1 {
			t.Fatalf("按字节定位原文失败：%+v %v", got, err)
		}
	}
	if err := e.Add(Document{ID: "3", Title: "新增文章", Content: "uniquetoken\n第二段"}); err != nil {
		t.Fatal(err)
	}
	restored, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.Search("uniquetoken")
	if err != nil || got.Total != 1 || got.Results[0].Document.ID != "3" || restored.Count() != 3 {
		t.Fatal("追加及重建后定位错误", err)
	}
}

func TestReadOnlyRequestedPostings(t *testing.T) {
	e := testEngine(t, []Document{
		{ID: "1", Title: "apple", Content: "apple"},
		{ID: "2", Title: "banana", Content: "banana"},
	}, 1)
	// 损坏无关词的倒排段，查询 apple 不应读取或解码 banana 的记录。
	file, err := os.OpenFile(e.indexPath, os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	for _, location := range e.terms["banana"] {
		if _, err := file.WriteAt([]byte(strings.Repeat("!", location.Length)), location.Offset); err != nil {
			t.Fatal(err)
		}
	}
	file.Close()
	got, err := e.Search("apple")
	if err != nil || got.Total != 1 {
		t.Fatalf("不应读取无关倒排段：%v", err)
	}
	if _, err := e.Search("banana"); err == nil {
		t.Fatal("读取损坏的目标倒排段应返回错误")
	}
}

func TestBatchByteLimit(t *testing.T) {
	padding := strings.Repeat(" ", 600000)
	text := padding + `{"id":"1","title":"Go","content":"channel"}` + "\n" + padding + `{"id":"2","title":"Go","content":"channel"}` + "\n"
	path := filepath.Join(t.TempDir(), "documents.jsonl")
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	e, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if len(e.terms["go"]) != 2 {
		t.Fatal("未按原始字节数分批写盘")
	}
	got, err := e.Search("channel")
	if err != nil || got.Total != 2 {
		t.Fatal("跨段查询失败", err)
	}
}

func TestTokenize(t *testing.T) {
	for input, want := range map[string][]string{"数据库": {"数据库"}, " Go， 并发！ ": {"go", "并发"}, "，。！？ ": {}} {
		if got := tokenize(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: got=%v want=%v", input, got, want)
		}
	}
}
