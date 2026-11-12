package main

import (
	"bufio"
	"container/heap"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type NameCount struct {
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

// 比较规则；次数相同时按字符串序，不同时按次数
func betterName(a, b NameCount) bool {
	if a.Count != b.Count {
		return a.Count > b.Count
	}
	return a.Name < b.Name
}

type nameHeap []NameCount

func (h *nameHeap) Len() int           { return len(*h) }
func (h *nameHeap) Less(i, j int) bool { return betterName((*h)[j], (*h)[i]) }
func (h *nameHeap) Swap(i, j int)      { (*h)[i], (*h)[j] = (*h)[j], (*h)[i] }
func (h *nameHeap) Push(x any)         { *h = append(*h, x.(NameCount)) }
func (h *nameHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	old[len(old)-1] = NameCount{}
	*h = old[:len(old)-1]
	return x
}

func addCandidate(h *nameHeap, candidate NameCount, k int) {
	if h.Len() < k {
		heap.Push(h, candidate)
	} else if betterName(candidate, (*h)[0]) {
		(*h)[0] = candidate
		heap.Fix(h, 0)
	}
}

func countNames(r io.Reader) (map[string]uint64, uint64, error) {
	counts := make(map[string]uint64)
	var total uint64
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		counts[name]++
		total++
		if len(counts) > 1_000_000 {
			return nil, 0, fmt.Errorf("单桶不同姓名超过 100 万，请增加桶数")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	return counts, total, nil
}

// 将姓名按哈希分到 bucketCount 个文件，同名一定进入同一桶
func splitNames(inputPath, directory string, bucketCount int) (paths []string, err error) {
	input, err := os.Open(inputPath)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	files := make([]*os.File, 0, bucketCount)
	writers := make([]*bufio.Writer, 0, bucketCount)
	defer func() {
		for i, file := range files {
			err = errors.Join(err, writers[i].Flush(), file.Close())
		}
	}()
	for i := 0; i < bucketCount; i++ {
		path := filepath.Join(directory, fmt.Sprintf("bucket-%03d.txt", i))
		file, openErr := os.Create(path)
		if openErr != nil {
			return nil, openErr
		}
		paths = append(paths, path)
		files = append(files, file)
		writers = append(writers, bufio.NewWriter(file))
	}
	hasher := fnv.New64a()
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if name == "" {
			continue
		}
		hasher.Reset()
		hasher.Write([]byte(name))
		bucket := int(hasher.Sum64() % uint64(bucketCount))
		if _, err := writers[bucket].WriteString(name + "\n"); err != nil {
			return nil, err
		}
	}
	return paths, scanner.Err()
}

type NameResult struct {
	Total     uint64      `json:"total"`      //有效记录数
	Unique    int         `json:"unique"`     //不同名字数
	Top       []NameCount `json:"top"`        //排行
	ElapsedMS float64     `json:"elapsed_ms"` //耗时
}

// 分块 → 逐桶计数 → 同一个全局堆 → 返回结果
func analyzeNames(path string, k int, bucketCount int) (NameResult, error) {
	started := time.Now()
	result := NameResult{Top: []NameCount{}}
	if k < 1 || k > 1000 || bucketCount < 1 || bucketCount > 256 {
		return result, fmt.Errorf("K 范围 1–1000，桶数范围 1–256")
	}

	tempDirectory := filepath.Join(filepath.Dir(path), "tmp")
	if err := os.MkdirAll(tempDirectory, 0755); err != nil {
		return result, err
	}
	directory, err := os.MkdirTemp(tempDirectory, "search-engine-")
	if err != nil {
		return result, err
	}
	// 清理本次创建的临时目录
	defer os.RemoveAll(directory)

	//分块，得到存储每块路径的切片
	paths, err := splitNames(path, directory, bucketCount)
	if err != nil {
		return result, err
	}

	//创建全局最小堆
	h := nameHeap{}

	//循环遍历每块，确保不超内存
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return result, err
		}

		//统计每一块的名字计数，得到当前桶中每个姓名的次数、当前桶的有效记录总数、错误。
		counts, total, readErr := countNames(file)
		closeErr := file.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return result, err
		}

		result.Total += total
		result.Unique += len(counts)
		for name, count := range counts {
			//把每个桶的结果加入容量为k的全局最小堆
			addCandidate(&h, NameCount{Name: name, Count: count}, k)
		}
	}
	result.Top = make([]NameCount, h.Len())
	for i := len(result.Top) - 1; i >= 0; i-- {
		result.Top[i] = heap.Pop(&h).(NameCount)
	}
	result.ElapsedMS = float64(time.Since(started).Microseconds()) / 1000
	return result, nil
}
