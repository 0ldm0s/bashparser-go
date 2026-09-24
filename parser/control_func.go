package parser

import "strings"

// control_func.go：控制结构 II（对齐 bashParser.ts 3441-3697 行）：
// parseCasePattern / parseCasePatternSegmented / parseFunction /
// parseDeclaration / parseUnset / consumeKeyword。

// parseCasePattern case 模式解析（对齐 parseCasePattern）
func parseCasePattern(p *ParseState) []*TsNode {
	skipBlanks(p.L)
	save := saveLex(p.L)
	start := p.L.b
	startI := p.L.i
	parenDepth := 0
	hasDollar := false
	hasBracketOutsideParen := false
	hasQuote := false
	for p.L.i < p.L.len {
		c := peek(p.L, 0)
		if c == '\\' && p.L.i+1 < p.L.len {
			// 转义字符——两者一起消费（`bar\ baz` 视为单模式）；
			// \<newline> 是续行：吃掉但仍在模式内
			advance(p.L)
			advance(p.L)
			continue
		}
		if c == '"' || c == '\'' {
			hasQuote = true
			// 跳过引号段，使其内容（空格、| 等）不破坏前瞻扫描
			advance(p.L)
			for p.L.i < p.L.len && peek(p.L, 0) != c {
				if peek(p.L, 0) == '\\' && p.L.i+1 < p.L.len {
					advance(p.L)
				}
				advance(p.L)
			}
			if peek(p.L, 0) == c {
				advance(p.L)
			}
			continue
		}
		// 括号计数：模式内任何 ( 开启作用域，平衡前不在 ) 或 | 断开。
		// 处理 extglob *(a|b) 与嵌套形态 *([0-9])([0-9])
		if c == '(' {
			parenDepth++
			advance(p.L)
			continue
		}
		if parenDepth > 0 {
			if c == ')' {
				parenDepth--
				advance(p.L)
				continue
			}
			if c == '\n' {
				break
			}
			advance(p.L)
			continue
		}
		if c == ')' || c == '|' || c == ' ' || c == '\t' || c == '\n' {
			break
		}
		if c == '$' {
			hasDollar = true
		}
		if c == '[' {
			hasBracketOutsideParen = true
		}
		advance(p.L)
	}
	if p.L.b == start {
		return nil
	}
	text := string(p.srcRunes[startI:p.L.i])
	hasExtglobParen := containsExtglobParen(text)
	// 模式中的引号段：tree-sitter 在引号边界切分为多个兄弟节点。
	// `*"foo"*` → (extglob_pattern)(string)(extglob_pattern)。用分段
	// 扫描重扫
	if hasQuote && !hasExtglobParen {
		restoreLex(p.L, save)
		return parseCasePatternSegmented(p)
	}
	// 含 [ 或 $ 的模式经 word 解析切分为 concatenation，除非模式有
	// extglob 括号（有则覆盖产出 extglob_pattern）。
	// `*.[1357]` → concat(word word number word)；`${PN}.pot` →
	// concat(expansion word)；`*([0-9])` → extglob_pattern
	if !hasExtglobParen && (hasDollar || hasBracketOutsideParen) {
		restoreLex(p.L, save)
		if w := parseWord(p, CtxArg); w != nil {
			return []*TsNode{w}
		}
		return nil
	}
	// 以 extglob 操作符字符（+ - ? * @ !）开头后随标识符字符的模式是
	// extglob_pattern——即使无括号或 glob 元字符。`-o)` → extglob_pattern；
	// 普通 `foo)` → word
	typ := "word"
	if hasExtglobParen || strings.ContainsAny(text, "*?") || isExtglobOpIdentPrefix(text) {
		typ = "extglob_pattern"
	}
	return []*TsNode{mk(p, typ, start, p.L.b, nil)}
}

// containsExtglobParen 对齐正则 /[*?+@!]\(/（extglob 形态检测）
func containsExtglobParen(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		c := s[i]
		if (c == '*' || c == '?' || c == '+' || c == '@' || c == '!') && s[i+1] == '(' {
			return true
		}
	}
	return false
}

// parseCasePatternSegmented 含引号 case 模式的分段扫描（对齐
// parseCasePatternSegmented）：`*"foo"*` → [extglob_pattern, string,
// extglob_pattern]。裸段含 */? 为 extglob_pattern，否则 word。
// 引号外停在 ) | 空格 tab 换行
func parseCasePatternSegmented(p *ParseState) []*TsNode {
	var parts []*TsNode
	segStart := p.L.b
	segStartI := p.L.i
	flushSeg := func() {
		if p.L.i > segStartI {
			t := string(p.srcRunes[segStartI:p.L.i])
			typ := "word"
			if strings.ContainsAny(t, "*?") {
				typ = "extglob_pattern"
			}
			parts = append(parts, mk(p, typ, segStart, p.L.b, nil))
		}
	}
	for p.L.i < p.L.len {
		c := peek(p.L, 0)
		if c == '\\' && p.L.i+1 < p.L.len {
			advance(p.L)
			advance(p.L)
			continue
		}
		if c == '"' {
			flushSeg()
			parts = append(parts, parseDoubleQuoted(p))
			segStart = p.L.b
			segStartI = p.L.i
			continue
		}
		if c == '\'' {
			flushSeg()
			tok := NextToken(p.L, CtxArg)
			parts = append(parts, leaf(p, "raw_string", tok))
			segStart = p.L.b
			segStartI = p.L.i
			continue
		}
		if c == ')' || c == '|' || c == ' ' || c == '\t' || c == '\n' {
			break
		}
		advance(p.L)
	}
	flushSeg()
	return parts
}

// parseFunction function 定义（对齐 parseFunction）
func parseFunction(p *ParseState, fnTok Token) *TsNode {
	fnKw := leaf(p, "function", fnTok)
	skipBlanks(p.L)
	nameTok := NextToken(p.L, CtxArg)
	name := mk(p, "word", nameTok.Start, nameTok.End, nil)
	kids := []*TsNode{fnKw, name}
	skipBlanks(p.L)
	if peek(p.L, 0) == '(' && peek(p.L, 1) == ')' {
		o := NextToken(p.L, CtxCmd)
		c := NextToken(p.L, CtxCmd)
		kids = append(kids, leaf(p, "(", o))
		kids = append(kids, leaf(p, ")", c))
	}
	skipBlanks(p.L)
	skipNewlines(p)
	body := parseCommand(p)
	if body != nil {
		// redirected_statement(compound_statement, ...) 的重定向按
		// grammar 提升到 function_definition 层
		if body.Type == "redirected_statement" &&
			len(body.Children) >= 2 &&
			body.Children[0].Type == "compound_statement" {
			kids = append(kids, body.Children...)
		} else {
			kids = append(kids, body)
		}
	}
	last := kids[len(kids)-1]
	return mk(p, "function_definition", fnKw.StartIndex, last.EndIndex, kids)
}

// parseDeclaration 声明命令 export/declare/typeset/readonly/local
//（对齐 parseDeclaration）
func parseDeclaration(p *ParseState, kwTok Token) *TsNode {
	kw := leaf(p, kwTok.Value, kwTok)
	kids := []*TsNode{kw}
	for {
		skipBlanks(p.L)
		c := peek(p.L, 0)
		if c == 0 || c == '\n' || c == ';' || c == '&' || c == '|' ||
			c == ')' || c == '<' || c == '>' {
			break
		}
		if a := tryParseAssignment(p); a != nil {
			kids = append(kids, a)
			continue
		}
		// 引号串或拼接：`export "FOO=bar"`、`export 'X'`
		if c == '"' || c == '\'' || c == '$' {
			if w := parseWord(p, CtxArg); w != nil {
				kids = append(kids, w)
				continue
			}
			break
		}
		// 标志（-a）或裸变量名
		save := saveLex(p.L)
		tok := NextToken(p.L, CtxArg)
		if tok.Type == TokWord || tok.Type == TokNumber {
			first := rune(0)
			if tok.Value != "" {
				first = rune(tok.Value[0])
			}
			if len(tok.Value) > 0 && tok.Value[0] == '-' {
				kids = append(kids, leaf(p, "word", tok))
			} else if isIdentStart(first) {
				kids = append(kids, mk(p, "variable_name", tok.Start, tok.End, nil))
			} else {
				kids = append(kids, leaf(p, "word", tok))
			}
		} else {
			restoreLex(p.L, save)
			break
		}
	}
	last := kids[len(kids)-1]
	return mk(p, "declaration_command", kw.StartIndex, last.EndIndex, kids)
}

// parseUnset unset/unsetenv 命令（对齐 parseUnset）
func parseUnset(p *ParseState, kwTok Token) *TsNode {
	kw := leaf(p, "unset", kwTok)
	kids := []*TsNode{kw}
	for {
		skipBlanks(p.L)
		c := peek(p.L, 0)
		if c == 0 || c == '\n' || c == ';' || c == '&' || c == '|' ||
			c == ')' || c == '<' || c == '>' {
			break
		}
		// 安全：用 parseWord（非裸 nextToken），使引号串如
		// `unset 'a[$(id)]]'` 产出 raw_string 子节点供 ast.ts 拒绝。
		// 此前 break 静默丢弃非 WORD 参数——把算术下标代码执行向量
		// 藏出安全遍历器
		arg := parseWord(p, CtxArg)
		if arg == nil {
			break
		}
		if arg.Type == "word" {
			if len(arg.Text) > 0 && arg.Text[0] == '-' {
				kids = append(kids, arg)
			} else {
				kids = append(kids, mk(p, "variable_name", arg.StartIndex, arg.EndIndex, nil))
			}
		} else {
			kids = append(kids, arg)
		}
	}
	last := kids[len(kids)-1]
	return mk(p, "unset_command", kw.StartIndex, last.EndIndex, kids)
}

// consumeKeyword 消费期望关键字（对齐 consumeKeyword；匹配则产出
// 叶子，否则回退）
func consumeKeyword(p *ParseState, name string, kids *[]*TsNode) {
	skipNewlines(p)
	save := saveLex(p.L)
	t := NextToken(p.L, CtxCmd)
	if t.Type == TokWord && t.Value == name {
		*kids = append(*kids, leaf(p, name, t))
	} else {
		restoreLex(p.L, save)
	}
}
