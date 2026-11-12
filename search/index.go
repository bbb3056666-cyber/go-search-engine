package search

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const documentsPerBatch = 1000
const sourceBytesPerBatch = 1 << 20

// 一个词的一条倒排记录，同时保留标题和正文词频
type posting struct {
	ID      string
	TitleTF int
	BodyTF  int
}

type batchIndex map[string][]posting

func Load(path string) (*Engine, error) { return build(path, documentsPerBatch) }

// 每批最多指定篇数或约 1MiB 原始字节，各词倒排表分别编码写盘
func build(path string, batchSize int) (engine *Engine, err error) {
	source, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer source.Close()

	// 创建索引目录，每次启动覆盖重建倒排文件
	directory := filepath.Join(filepath.Dir(path), "tmp")
	if err := os.MkdirAll(directory, 0755); err != nil {
		return nil, err
	}
	index, err := os.Create(filepath.Join(directory, "postings.jsonl"))
	if err != nil {
		return nil, err
	}
	defer func() {
		closeErr := index.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(index.Name())
			engine = nil
		}
	}()

	// 初始化词典、原文位置、文章长度和全局统计，后面边读边填充
	engine = &Engine{
		sourcePath: path, indexPath: index.Name(),
		terms: make(map[string][]Location), documents: make(map[string]Location),
		lengths: make(map[string]fieldLengths),
		title:   fieldStats{DF: make(map[string]int)}, body: fieldStats{DF: make(map[string]int)},
	}
	batch := make(batchIndex)
	count, size := 0, 0
	reader := bufio.NewReader(source)
	var offset int64

	// 逐行读取原文，offset 记录当前行在文件中的起始字节位置
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		if len(strings.TrimSpace(string(line))) > 0 {
			var doc Document
			if err := json.Unmarshal(line, &doc); err != nil {
				return nil, err
			}
			if strings.TrimSpace(doc.ID) == "" || strings.TrimSpace(doc.Title) == "" || strings.TrimSpace(doc.Content) == "" {
				return nil, fmt.Errorf("文档 id、title、content 不能为空")
			}
			if engine.Has(doc.ID) {
				return nil, fmt.Errorf("重复文档 ID：%s", doc.ID)
			}

			// 当前批次达到篇数上限，或加入这篇会超过字节上限，就先写盘并清空批次
			if count > 0 && (count >= batchSize || size+len(line) > sourceBytesPerBatch) {
				if err := engine.writeBatch(index, batch); err != nil {
					return nil, err
				}
				batch = make(batchIndex)
				count, size = 0, 0
			}

			// 保存原文位置，再把这篇文章的词频加入当前批次
			engine.documents[doc.ID] = Location{Offset: offset, Length: len(line)}
			engine.indexDocument(batch, doc)
			count++
			size += len(line)
		}

		// 空行和换行符也占字节，计算下一行位置时一起加上
		offset += int64(len(line))
		if readErr == io.EOF {
			break
		}
	}

	// 文件读完后，把最后不足一批的倒排记录也写入磁盘
	if count > 0 {
		if err := engine.writeBatch(index, batch); err != nil {
			return nil, err
		}
	}
	return engine, nil
}

// 每篇文章先分别计数，再按唯一词更新 df，重复出现不重复增加 df
func (e *Engine) indexDocument(batch batchIndex, doc Document) {
	// 标题和正文分别分词，保存词元数，并累计全局总长度
	title, body := tokenize(doc.Title), tokenize(doc.Content)
	e.lengths[doc.ID] = fieldLengths{Title: len(title), Body: len(body)}
	e.title.TotalLength += len(title)
	e.body.TotalLength += len(body)

	// 先统计这篇文章里，每个词在标题和正文分别出现几次
	frequencies := make(map[string]posting)
	for _, term := range title {
		item := frequencies[term]
		item.TitleTF++
		frequencies[term] = item
	}
	for _, term := range body {
		item := frequencies[term]
		item.BodyTF++
		frequencies[term] = item
	}

	// 每个词只遍历一次，加入倒排表；同一字段出现多次，df 仍只加一
	for term, item := range frequencies {
		term = strings.Clone(term)
		item.ID = doc.ID
		batch[term] = append(batch[term], item)
		if item.TitleTF > 0 {
			e.title.DF[term]++
		}
		if item.BodyTF > 0 {
			e.body.DF[term]++
		}
	}
}

func (e *Engine) writeBatch(file *os.File, batch batchIndex) error {
	// 每个词单独编码为一段，后续查询可以按词读取，不用解码整个批次
	for term, postings := range batch {
		data, err := json.Marshal(postings)
		if err != nil {
			return err
		}
		location, err := appendData(file, append(data, '\n'))
		if err != nil {
			return err
		}

		// 把这一段的偏移量和字节长度加入内存词典，保留该词已有的位置
		e.terms[term] = append(e.terms[term], location)
	}
	return nil
}
