package search

import (
	"container/heap"
	"encoding/json"
	"math"
	"os"
	"sort"
)

const heapSize = 20

// tf、df、n、length、average 分别为词频、文档频率、总篇数、字段长度、平均字段长度
func bm25(tf, df, n, length int, average float64) float64 {
	if tf <= 0 || n <= 0 || average <= 0 {
		return 0
	}
	const k1, b = 1.2, 0.75
	idf := math.Log(1 + (float64(n-df)+0.5)/(float64(df)+0.5))
	f := float64(tf)
	return idf * f * (k1 + 1) / (f + k1*(1-b+b*float64(length)/average))
}

// 堆只保存 ID 和分数，筛选完成后再读取原文
type documentScore struct {
	ID    string
	Score float64
}

func betterScore(a, b documentScore) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.ID < b.ID
}

type scoreHeap []documentScore

func (h *scoreHeap) Len() int           { return len(*h) }
func (h *scoreHeap) Less(i, j int) bool { return betterScore((*h)[j], (*h)[i]) }
func (h *scoreHeap) Swap(i, j int)      { (*h)[i], (*h)[j] = (*h)[j], (*h)[i] }
func (h *scoreHeap) Push(x any)         { *h = append(*h, x.(documentScore)) }
func (h *scoreHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	old[len(old)-1] = documentScore{}
	*h = old[:len(old)-1]
	return x
}

func (e *Engine) Search(query string) (Response, error) {
	response := Response{Results: []Result{}}
	index, err := os.Open(e.indexPath)
	if err != nil {
		return response, err
	}
	defer index.Close()

	// 保存所有候选的累计分数，重复查询词只处理一次
	scores := make(map[string]float64)
	seen := make(map[string]bool)

	// 从内存取全局篇数和总长度，算出标题、正文的平均词元数
	n := e.Count()
	titleAverage := float64(e.title.TotalLength) / float64(max(1, n))
	bodyAverage := float64(e.body.TotalLength) / float64(max(1, n))

	// 查询使用和建索引相同的分词规则，再去掉重复词
	for _, term := range tokenize(query) {
		if seen[term] {
			continue
		}
		seen[term] = true

		// 从内存词典取得这个词的各段位置，只读取这些倒排段
		for _, location := range e.terms[term] {
			data, err := readData(index, location)
			if err != nil {
				return response, err
			}
			var postings []posting
			if err := json.Unmarshal(data, &postings); err != nil {
				return response, err
			}

			// 词频来自倒排记录，df 和文章长度来自内存，标题乘二后加到该文章总分
			for _, item := range postings {
				lengths := e.lengths[item.ID]
				scores[item.ID] += 2*bm25(item.TitleTF, e.title.DF[term], n, lengths.Title, titleAverage) + bm25(item.BodyTF, e.body.DF[term], n, lengths.Body, bodyAverage)
			}
		}
	}

	// 所有词的分数累计完后，map 中的文章数就是总命中数
	response.Total = len(scores)
	h := scoreHeap{}

	// 遍历候选，用容量固定的最小堆保留前二十个 ID 和分数
	for id, score := range scores {
		candidate := documentScore{ID: id, Score: score}
		if h.Len() < heapSize {
			heap.Push(&h, candidate)
		} else if betterScore(candidate, h[0]) {
			h[0] = candidate
			heap.Fix(&h, 0)
		}
	}

	// 堆只保证堆顶排名最靠后，返回前还要把入选结果按分数排序
	sort.Slice(h, func(i, j int) bool { return betterScore(h[i], h[j]) })
	if len(h) == 0 {
		return response, nil
	}

	// 筛选完成再打开原文文件，只读取入选文章的标题和正文
	source, err := os.Open(e.sourcePath)
	if err != nil {
		return response, err
	}
	defer source.Close()
	for _, item := range h {
		doc, err := e.readDocument(source, item.ID)
		if err != nil {
			return response, err
		}
		response.Results = append(response.Results, Result{Document: doc, Score: item.Score})
	}
	return response, nil
}
