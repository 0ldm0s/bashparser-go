package parser

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestAgainstTSReference 与上游 treeSitterAnalysis.ts 直拍（基准由
// evidence/c6c1-reference.mjs 在 eva-cli 作用域内 tsx 直跑上游源码生成，
// 28 条覆盖上游注释锚点场景）。逐条比对 AnalyzeCommand 输出。
func TestAgainstTSReference(t *testing.T) {
	data, err := os.ReadFile("../fixtures/c6c1-reference.jsonl")
	if err != nil {
		t.Fatalf("读取对拍基准失败: %v", err)
	}
	entries := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		entries++
		var entry struct {
			Input    string    `json:"input"`
			Analysis *Analysis `json:"analysis"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("基准行解析失败: %v\n%s", err, line)
		}
		root := ParseSource(entry.Input, 0)
		if root == nil {
			t.Fatalf("%q AST 解析失败", entry.Input)
		}
		got := AnalyzeCommand(root, entry.Input)
		if !reflect.DeepEqual(got, *entry.Analysis) {
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(entry.Analysis)
			t.Errorf("%q 分析与上游不一致:\n  期望 %s\n  实际 %s",
				entry.Input, wantJSON, gotJSON)
		}
	}
	if entries != 28 {
		t.Errorf("基准应 28 条，实际 %d 条", entries)
	}
}
