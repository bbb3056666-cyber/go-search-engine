package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"searchEngine/search"
	"strings"
	"testing"
)

func testEngine(t *testing.T) (*search.Engine, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docs.jsonl")
	data := "{\"id\":\"A\",\"title\":\"Go\",\"content\":\"channel channel\"}\n" + "{\"id\":\"B\",\"title\":\"实践\",\"content\":\"go channel\"}"
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	engine, err := search.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	return engine, path
}
func mustSearch(t *testing.T, engine *search.Engine, query string) search.Response {
	t.Helper()
	result, err := engine.Search(query)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	if err := os.WriteFile(path, []byte("张伟\n李娜\n张伟"), 0644); err != nil {
		t.Fatal(err)
	}
	engine, _ := testEngine(t)
	h := newHandler(engine, path)
	for _, item := range []struct {
		method, path string
		status       int
	}{{"GET", "/", 200}, {"GET", "/api/search?q=go", 200}, {"GET", "/api/search?q=", 400}, {"POST", "/api/names", 200}, {"POST", "/api/names?k=0", 400}, {"POST", "/api/names?k=1001", 400}, {"POST", "/api/names?k=bad", 400}, {"GET", "/missing", 404}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(item.method, item.path, nil))
		if w.Code != item.status {
			t.Errorf("%s: %d", item.path, w.Code)
		}
	}
}
func TestAddingAndPersistence(t *testing.T) {
	engine, documentsPath := testEngine(t)
	namesPath := filepath.Join(t.TempDir(), "names.txt")
	if err := os.WriteFile(namesPath, []byte("张伟\n李娜\n张伟"), 0644); err != nil {
		t.Fatal(err)
	}
	h := newHandler(engine, namesPath)
	post := func(path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w
	}
	post("/api/names/add", `{"name":" 张伟 "}`, 201)
	post("/api/names/add", `{"name":" "}`, 400)
	post("/api/names/add", `{"name":"甲\n乙"}`, 400)
	w := post("/api/names?k=1", "", 200)
	var ranking NameResult
	if err := json.Unmarshal(w.Body.Bytes(), &ranking); err != nil {
		t.Fatal(err)
	}
	if ranking.Total != 4 || ranking.Unique != 2 || len(ranking.Top) != 1 || ranking.Top[0].Count != 3 {
		t.Fatalf("新增姓名未正确计入：%+v", ranking)
	}
	// 原文件没有结尾换行，新文章包含正文换行；保存后必须仍是合法 JSONL。
	post("/api/documents", `{"title":"新增文章","content":"lessonuniquetoken\n第二段"}`, 201)
	if got := mustSearch(t, engine, "lessonuniquetoken"); got.Total != 1 || got.Results[0].Document.Title != "新增文章" {
		t.Fatal("新增文章应立即可搜")
	}
	restored, err := search.Load(documentsPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restored.Close() })
	if restored.Count() != 3 || mustSearch(t, restored, "lessonuniquetoken").Total != 1 {
		t.Fatal("新增文章未正确持久化")
	}
	post("/api/documents", `{"title":"","content":"正文"}`, 400)
	if engine.Count() != 3 {
		t.Fatal("无效文章不应加入索引")
	}
}
