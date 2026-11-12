package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"searchEngine/search"

	"github.com/gin-gonic/gin"
)

//go:embed web/index.html
var page []byte

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "监听地址")
	flag.Parse()

	// 读取文章文件，重建磁盘索引和内存映射
	engine, err := search.Load("data/documents.jsonl")
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("文章：%d 篇", engine.Count())
	log.Printf("地址：http://%s", *addr)

	// 注册接口并启动服务，页面也由 Gin 提供
	router := newHandler(engine, "data/names.txt")
	err = router.Run(*addr)
	engine.Close()
	if err != nil {
		log.Fatal(err)
	}
}

func newHandler(engine *search.Engine, namesPath string) *gin.Engine {
	router := gin.Default()
	router.SetTrustedProxies(nil)

	// 禁用响应缓存，添加数据的请求只允许来自同站页面
	router.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if c.Request.Method == "POST" {
			if origin := c.GetHeader("Origin"); origin != "" && origin != "http://"+c.Request.Host && origin != "https://"+c.Request.Host {
				c.AbortWithStatusJSON(403, gin.H{"error": "不允许跨站请求"})
				return
			}
		}
		c.Next()
	})

	var documentsMu sync.RWMutex // 查询和添加文章共用这把锁

	// 返回内嵌的前端页面
	router.GET("/", func(c *gin.Context) {
		c.Data(200, "text/html; charset=utf-8", page)
	})

	// 查询姓名前 K 名，默认返回前 100 名
	router.POST("/api/names", func(c *gin.Context) {
		k := 100
		if raw := c.Query("k"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 1000 {
				c.JSON(400, gin.H{"error": "N 必须是 1–1000 的整数"})
				return
			}
			k = n
		}

		// 将姓名分到 64 个桶，逐桶计数，再用全局堆筛选
		result, err := analyzeNames(namesPath, k, 64)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, result)
	})

	// 接收关键词，返回总命中数和相关性最高的前 20 篇文章
	router.GET("/api/search", func(c *gin.Context) {
		query := strings.TrimSpace(c.Query("q"))
		if query == "" || len([]rune(query)) > 200 {
			c.JSON(400, gin.H{"error": "请输入 1–200 字关键词"})
			return
		}

		documentsMu.RLock()
		result, err := engine.Search(query)
		documentsMu.RUnlock()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, result)
	})

	// 校验姓名后追加到文件，下次统计时计入
	router.POST("/api/names/add", func(c *gin.Context) {
		var body struct {
			Name string `json:"name"`
		}

		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(400, gin.H{"error": "姓名格式无效"})
			return
		}

		name := strings.TrimSpace(body.Name)
		if name == "" || len([]rune(name)) > 100 || strings.ContainsAny(name, "\r\n") {
			c.JSON(400, gin.H{"error": "姓名须为 1–100 字，不能包含换行"})
			return
		}

		if err := appendLine(namesPath, name); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		c.JSON(201, gin.H{"name": name})
	})

	// 校验文章后保存原文、追加倒排段，并更新内存统计
	router.POST("/api/documents", func(c *gin.Context) {
		var doc search.Document
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		if err := c.ShouldBindJSON(&doc); err != nil {
			c.JSON(400, gin.H{"error": "文章格式无效或内容过大"})
			return
		}

		doc.Title, doc.Content = strings.TrimSpace(doc.Title), strings.TrimSpace(doc.Content)
		if doc.Title == "" || doc.Content == "" || len([]rune(doc.Title)) > 500 || len([]rune(doc.Content)) > 100000 {
			c.JSON(400, gin.H{"error": "标题须为 1–500 字，正文须为 1–10 万字"})
			return
		}

		documentsMu.Lock()
		defer documentsMu.Unlock()

		// 从当前篇数往后查找，生成尚未使用的文章 ID
		for n := engine.Count() + 1; ; n++ {
			doc.ID = "doc-" + strconv.Itoa(n)
			if !engine.Has(doc.ID) {
				break
			}
		}

		if err := engine.Add(doc); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		c.JSON(201, doc)
	})

	return router
}

// 追加一行数据，原文件末尾没有换行时先补上
func appendLine(path, line string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	info, err := file.Stat()
	if err == nil && info.Size() > 0 {
		last := make([]byte, 1)
		if _, err = file.ReadAt(last, info.Size()-1); err == nil && last[0] != '\n' {
			line = "\n" + line
		}
	}

	if err == nil {
		_, err = fmt.Fprintln(file, line)
	}

	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
