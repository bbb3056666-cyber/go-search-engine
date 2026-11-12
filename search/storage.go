package search

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func appendData(file *os.File, data []byte) (Location, error) {
	// 定位到文件末尾，追加数据，返回写入起点和实际字节数
	offset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return Location{}, err
	}
	n, err := file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return Location{Offset: offset, Length: n}, err
}

func readData(file *os.File, location Location) ([]byte, error) {
	// 按记录的偏移量和长度读取指定范围，不扫描前面的数据
	data := make([]byte, location.Length)
	_, err := file.ReadAt(data, location.Offset)
	return data, err
}

// Add 追加原文和新的倒排段，已有段的位置保持不变
func (e *Engine) Add(doc Document) error {
	if e.Has(doc.ID) {
		return fmt.Errorf("重复文档 ID：%s", doc.ID)
	}
	source, err := os.OpenFile(e.sourcePath, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer source.Close()
	index, err := os.OpenFile(e.indexPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer index.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}

	// 原文最后一行如果没有换行，先补上，避免两篇文章连在同一行
	if info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := source.ReadAt(last, info.Size()-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			if _, err := source.Write([]byte{'\n'}); err != nil {
				return err
			}
		}
	}

	// 将新文章编码成一行 JSON，追加到原文文件并取得位置
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	location, err := appendData(source, append(data, '\n'))
	if err != nil {
		return err
	}

	// 新文章单独作为一个批次，各词追加新段，同时更新长度和全局统计
	batch := make(batchIndex)
	e.indexDocument(batch, doc)
	if err := e.writeBatch(index, batch); err != nil {
		return err
	}

	// 保存新文章的位置，后面可以直接通过 ID 读取原文
	e.documents[doc.ID] = location
	return nil
}

func (e *Engine) readDocument(file *os.File, id string) (Document, error) {
	// 用 ID 查内存位置表，读取对应的一行原文，再解码成文章
	data, err := readData(file, e.documents[id])
	if err != nil {
		return Document{}, err
	}
	var doc Document
	err = json.Unmarshal(data, &doc)
	return doc, err
}
