package parser

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestAstAgainstTSReference 与上游 ast.ts 直拍（基准由
// evidence 流程的 c6c2-reference.mjs 在 eva-cli 作用域内 tsx 直跑
// ast.ts 副本生成——副本仅替换 parser.ts 依赖为语义一致的 stub，
// 79 条覆盖预检查六类、作用域语义、赋值防线、算术、占位符、heredoc、
// eval-like/zsh/jq、包装剥离 fail-closed、grammar quirk 全部分支）。
func TestAstAgainstTSReference(t *testing.T) {
	data, err := os.ReadFile("../fixtures/ast-reference.jsonl")
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
			Input     string                  `json:"input"`
			Parse     *ParseForSecurityResult `json:"parse"`
			Semantics *SemanticCheckResult    `json:"semantics"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("基准行解析失败: %v\n%s", err, line)
		}
		got := ParseForSecurity(entry.Input)
		if !reflect.DeepEqual(got, *entry.Parse) {
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(entry.Parse)
			t.Errorf("%q parseForSecurity 与上游不一致:\n  期望 %s\n  实际 %s",
				entry.Input, wantJSON, gotJSON)
			continue
		}
		// simple 命令比对 checkSemantics（上游对 simple 跑语义检查）
		if got.Kind == kindSimple {
			gotSem := CheckSemantics(got.Commands)
			if entry.Semantics != nil && !reflect.DeepEqual(gotSem, *entry.Semantics) {
				gotJSON, _ := json.Marshal(gotSem)
				wantJSON, _ := json.Marshal(entry.Semantics)
				t.Errorf("%q checkSemantics 与上游不一致:\n  期望 %s\n  实际 %s",
					entry.Input, wantJSON, gotJSON)
			}
		}
	}
	if entries != 79 {
		t.Errorf("基准应 79 条，实际 %d 条", entries)
	}
}
