// tsbash-poc：bashParser.ts Go 全量 1:1 直译的互拍验证项目。
//
// 用法：
//
//	go run . -mode ast -in fixtures/commands.jsonl -out fixtures/go-ast.jsonl
//	（TS 侧：在 eva-cli 作用域内 tsx ts/dump.mjs 生成同名 ts-ast.jsonl）
//	git diff --no-index 比对两侧——零差异为通过
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/0ldm0s/bashparser-go/parser"
)

type inputLine struct {
	Input string `json:"input"`
}

// outLine 输出行结构（键序对齐 TS JSON.stringify 插入序：input 先于 ast）
type outLine struct {
	Input string         `json:"input"`
	Ast   *parser.TsNode `json:"ast"`
}

type tokenOut struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

func main() {
	mode := flag.String("mode", "ast", "dump 模式：ast（互拍）| tokens（lexer 自查）")
	in := flag.String("in", "fixtures/commands.jsonl", "输入 JSONL")
	out := flag.String("out", "", "输出 JSONL（缺省 stdout）")
	flag.Parse()

	data, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读输入失败:", err)
		os.Exit(1)
	}

	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "建输出失败:", err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	bw := bufio.NewWriter(w)
	defer bw.Flush()

	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var il inputLine
		if err := json.Unmarshal([]byte(line), &il); err != nil {
			fmt.Fprintln(os.Stderr, "输入行解析失败:", err)
			os.Exit(1)
		}
		switch *mode {
		case "tokens":
			dumpTokens(bw, il.Input)
		case "ast":
			dumpAST(bw, il.Input)
		}
	}
}

// dumpAST 输出 AST JSONL（互拍对拍单位）
func dumpAST(w *bufio.Writer, input string) {
	root := parser.ParseSource(input, 0)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // 对齐 TS JSON.stringify（不转义 & < >）
	if err := enc.Encode(outLine{Input: input, Ast: root}); err != nil {
		fmt.Fprintln(os.Stderr, "序列化失败:", err)
		os.Exit(1)
	}
}

// dumpTokens 输出 token 流（Lexer 自查；TS 侧 nextToken 未导出，
// 跨侧对拍单位是 AST）
func dumpTokens(w *bufio.Writer, input string) {
	L := parser.MakeLexer(input)
	tokens := []tokenOut{}
	for {
		t := parser.NextToken(L, parser.CtxArg)
		tokens = append(tokens, tokenOut{t.Type, t.Value, t.Start, t.End})
		if t.Type == parser.TokEOF {
			break
		}
	}
	b, _ := json.Marshal(map[string]any{"input": input, "tokens": tokens})
	w.Write(b)
	w.WriteByte('\n')
}
