package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 全量排序作为参照，检查分块后的全局 Top K，不复用待测堆的比较函数。
func TestNameStatistics(t *testing.T) {
	var input strings.Builder
	wantCounts := map[string]uint64{}
	for i := 0; i < 300; i++ {
		name := fmt.Sprintf("姓名%03d", i)
		for j := 0; j < i%13+1; j++ {
			fmt.Fprintf(&input, " %s \r\n\n", name)
			wantCounts[name]++
		}
	}
	path := filepath.Join(t.TempDir(), "names.txt")
	if err := os.WriteFile(path, []byte(input.String()), 0644); err != nil {
		t.Fatal(err)
	}
	want := []NameCount{}
	var total uint64
	for name, count := range wantCounts {
		want = append(want, NameCount{Name: name, Count: count})
		total += count
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i].Count != want[j].Count {
			return want[i].Count > want[j].Count
		}
		return want[i].Name < want[j].Name
	})
	for _, k := range []int{1, 100, 400} {
		got, err := analyzeNames(path, k, 4)
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != total || got.Unique != 300 || !reflect.DeepEqual(got.Top, want[:min(k, len(want))]) {
			t.Fatalf("K=%d 结果不正确", k)
		}
	}
}

func TestNamesEmptyAndErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, []byte(" \n\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := analyzeNames(path, 100, 4)
	if err != nil || got.Total != 0 || len(got.Top) != 0 {
		t.Fatal("空文件处理不正确")
	}
	if _, err := analyzeNames(path, 0, 4); err == nil {
		t.Fatal("应拒绝非法 K")
	}
	if _, err := analyzeNames(path, 1, 0); err == nil {
		t.Fatal("应拒绝非法桶数")
	}
	if _, err := analyzeNames(path+"missing", 1, 4); err == nil {
		t.Fatal("应返回文件错误")
	}
	if _, _, err := countNames(strings.NewReader(strings.Repeat("a", 128*1024))); err == nil {
		t.Fatal("不应忽略读取错误")
	}
}
