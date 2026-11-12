package search

import "os"

type Document struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type Result struct {
	Document Document `json:"document"`
	Score    float64  `json:"score"`
}

type Response struct {
	Total   int      `json:"total"`
	Results []Result `json:"results"`
}

// Location 表示文件中一段数据的字节位置。
type Location struct {
	Offset int64
	Length int
}

type fieldLengths struct{ Title, Body int }
type fieldStats struct {
	DF          map[string]int
	TotalLength int
}

// Engine 在内存中保存位置映射、文章长度和全局统计。
// 查询与添加由调用方的读写锁隔离。
type Engine struct {
	sourcePath  string
	indexPath   string
	terms       map[string][]Location
	documents   map[string]Location
	lengths     map[string]fieldLengths
	title, body fieldStats
}

func (e *Engine) Count() int         { return len(e.documents) }
func (e *Engine) Has(id string) bool { _, ok := e.documents[id]; return ok }
func (e *Engine) Close() error       { return os.Remove(e.indexPath) }
