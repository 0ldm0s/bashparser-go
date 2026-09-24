package parser

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestEntryAgainstTSReference parser.ts/ParsedCommand.ts 入口级直拍
// （基准由 c6c2b-reference.mjs 在 eva-cli 作用域内 tsx 直跑上游副本
// 生成——副本仅替换 bun:bundle→feature stub（TREE_SITTER_BASH=true
// 走主门正面路径）、遥测→空实现、commands→stub（主门版不调用），
// 26 条覆盖命令形态/三态/参数提取/命令级视图四方法）。
func TestEntryAgainstTSReference(t *testing.T) {
	data, err := os.ReadFile("../fixtures/entry-reference.jsonl")
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
			Input string `json:"input"`
			// 上游 parseCommand 输出：null 或 {envVars, commandNode}；
			// envVars 为字符串数组（"FOO=1" 形态，对齐上游 extractEnvVars）
			ParseCommand *struct {
				EnvVars     []string `json:"envVars"`
				CommandNode *TsNode  `json:"commandNode"`
			} `json:"parseCommand"`
			ParseCommandRawKind string    `json:"parseCommandRawKind"`
			Args                *[]string `json:"args"`
			Parsed              *struct {
				PipeSegments              []string            `json:"pipeSegments"`
				WithoutOutputRedirections string              `json:"withoutOutputRedirections"`
				OutputRedirections        []OutputRedirection `json:"outputRedirections"`
				TreeSitterAnalysis        *Analysis           `json:"treeSitterAnalysis"`
				OriginalCommand           string              `json:"originalCommand"`
			} `json:"parsed"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("基准行解析失败: %v\n%s", err, line)
		}

		// ── parseCommand ──
		pc := ParseCommand(entry.Input)
		switch {
		case entry.ParseCommand == nil:
			if pc != nil {
				t.Errorf("%q parseCommand 应为 null", entry.Input)
			}
		case pc == nil:
			t.Errorf("%q parseCommand 不应为 null", entry.Input)
		default:
			if !reflect.DeepEqual(pc.EnvVars, entry.ParseCommand.EnvVars) {
				g, _ := json.Marshal(pc.EnvVars)
				w, _ := json.Marshal(entry.ParseCommand.EnvVars)
				t.Errorf("%q envVars:\n  期望 %s\n  实际 %s", entry.Input, w, g)
			}
			gotNode, _ := json.Marshal(pc.CommandNode)
			wantNode, _ := json.Marshal(entry.ParseCommand.CommandNode)
			if !reflect.DeepEqual(gotNode, wantNode) {
				t.Errorf("%q commandNode 与上游不一致:\n  期望 %s\n  实际 %s",
					entry.Input, wantNode, gotNode)
			}
		}

		// ── parseCommandRaw 三态 ──
		root, st := ParseCommandRaw(entry.Input)
		gotKind := "null"
		switch st {
		case GateAborted:
			gotKind = "aborted"
		case GateParsed:
			gotKind = root.Type
		}
		if gotKind != entry.ParseCommandRawKind {
			t.Errorf("%q rawKind 期望 %q 实得 %q",
				entry.Input, entry.ParseCommandRawKind, gotKind)
		}

		// ── extractCommandArguments ──
		switch {
		case entry.Args == nil:
			if pc != nil && pc.CommandNode != nil {
				t.Errorf("%q args 应为 null", entry.Input)
			}
		default:
			if pc == nil || pc.CommandNode == nil {
				t.Errorf("%q commandNode 缺失无法提取 args", entry.Input)
			} else if got := ExtractCommandArguments(pc.CommandNode); !reflect.DeepEqual(got, *entry.Args) {
				g, _ := json.Marshal(got)
				w, _ := json.Marshal(*entry.Args)
				t.Errorf("%q args:\n  期望 %s\n  实际 %s", entry.Input, w, g)
			}
		}

		// ── ParsedCommand 四方法 ──
		switch {
		case entry.Parsed == nil:
			if parsed := Parse(entry.Input); parsed != nil {
				t.Errorf("%q ParsedCommand.parse 应为 null", entry.Input)
			}
		default:
			parsed := Parse(entry.Input)
			if parsed == nil {
				t.Errorf("%q ParsedCommand.parse 不应为 null", entry.Input)
				continue
			}
			if !reflect.DeepEqual(parsed.PipeSegments(), entry.Parsed.PipeSegments) {
				g, _ := json.Marshal(parsed.PipeSegments())
				w, _ := json.Marshal(entry.Parsed.PipeSegments)
				t.Errorf("%q pipeSegments:\n  期望 %s\n  实际 %s", entry.Input, w, g)
			}
			if parsed.WithoutOutputRedirections() != entry.Parsed.WithoutOutputRedirections {
				t.Errorf("%q withoutOutputRedirections 期望 %q 实得 %q",
					entry.Input, entry.Parsed.WithoutOutputRedirections,
					parsed.WithoutOutputRedirections())
			}
			if !reflect.DeepEqual(parsed.OutputRedirections(), entry.Parsed.OutputRedirections) {
				g, _ := json.Marshal(parsed.OutputRedirections())
				w, _ := json.Marshal(entry.Parsed.OutputRedirections)
				t.Errorf("%q outputRedirections:\n  期望 %s\n  实际 %s", entry.Input, w, g)
			}
			gotAnalysis, _ := json.Marshal(parsed.TreeSitterAnalysis())
			wantAnalysis, _ := json.Marshal(entry.Parsed.TreeSitterAnalysis)
			if string(gotAnalysis) != string(wantAnalysis) {
				t.Errorf("%q treeSitterAnalysis:\n  期望 %s\n  实际 %s",
					entry.Input, wantAnalysis, gotAnalysis)
			}
			if parsed.OriginalCommand() != entry.Parsed.OriginalCommand {
				t.Errorf("%q originalCommand 期望 %q 实得 %q",
					entry.Input, entry.Parsed.OriginalCommand, parsed.OriginalCommand())
			}
		}
	}
	if entries != 26 {
		t.Errorf("基准应 26 条，实际 %d 条", entries)
	}
}
