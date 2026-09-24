package parser

// word.go：词解析（对齐 bashParser.ts 2003-2553 行）：
// parseWord / parseBareWord / parseDoubleQuoted / parseDollarLike。

// parseWord 词位置元素：裸词/字符串/展开/拼接（对齐 parseWord）。
// 相邻多片段时包装为 concatenation
func parseWord(p *ParseState, _ LexContext) *TsNode {
	skipBlanks(p.L)
	var parts []*TsNode
	for p.L.i < p.L.len {
		c := peek(p.L, 0)
		if c == 0 || c == ' ' || c == '\t' || c == '\n' || c == '\r' ||
			c == '|' || c == '&' || c == ';' || c == '(' || c == ')' {
			break
		}
		// < > 是重定向操作符，除非 <( >(（进程替换）
		if c == '<' || c == '>' {
			if peek(p.L, 1) == '(' {
				if ps := parseProcessSub(p); ps != nil {
					parts = append(parts, ps)
					continue
				}
			}
			break
		}
		if c == '"' {
			parts = append(parts, parseDoubleQuoted(p))
			continue
		}
		if c == '\'' {
			tok := NextToken(p.L, CtxArg)
			parts = append(parts, leaf(p, "raw_string", tok))
			continue
		}
		if c == '$' {
			c1 := peek(p.L, 1)
			if c1 == '\'' {
				tok := NextToken(p.L, CtxArg)
				parts = append(parts, leaf(p, "ansi_c_string", tok))
				continue
			}
			if c1 == '"' {
				// 本地化字符串：$ 叶子 + string 节点
				dTok := Token{Type: TokDollar, Value: "$",
					Start: p.L.b, End: p.L.b + 1}
				advance(p.L)
				parts = append(parts, leaf(p, "$", dTok))
				parts = append(parts, parseDoubleQuoted(p))
				continue
			}
			if c1 == '`' {
				// $ 后接反引号——tree-sitter 完全略过 $，仅产出
				// (command_substitution)。消费 $，下一轮处理反引号
				advance(p.L)
				continue
			}
			if exp := parseDollarLike(p); exp != nil {
				parts = append(parts, exp)
			}
			continue
		}
		if c == '`' {
			if p.inBacktick > 0 {
				break
			}
			if bt := parseBacktick(p); bt != nil {
				parts = append(parts, bt)
			}
			continue
		}
		// brace 表达式 {1..5} 或 {a,b,c}——仅当形似时
		if c == '{' {
			if be := tryParseBraceExpr(p); be != nil {
				parts = append(parts, be)
				continue
			}
			// 安全：`{` 紧接命令终止符（; | & 换行或 EOF）时是独立词——
			// 不得经 tryParseBraceLikeCat 吞掉行剩余。`echo {;touch /tmp/evil`
			// 必须在 `;` 切分，安全遍历器才能看到 `touch`
			nc := peek(p.L, 1)
			if nc == ';' || nc == '|' || nc == '&' || nc == '\n' || nc == 0 ||
				nc == ')' || nc == ' ' || nc == '\t' {
				bStart := p.L.b
				advance(p.L)
				parts = append(parts, mk(p, "word", bStart, p.L.b, nil))
				continue
			}
			// 其余按 tree-sitter 方式切为词片段
			if cat := tryParseBraceLikeCat(p); cat != nil {
				parts = append(parts, cat...)
				continue
			}
		}
		// 参数位孤立 `}` 是词（`echo }foo`）——parseBareWord 在 } 断开，
		// 这里处理
		if c == '}' {
			bStart := p.L.b
			advance(p.L)
			parts = append(parts, mk(p, "word", bStart, p.L.b, nil))
			continue
		}
		// [ ] 是单字符词片段（tree-sitter 在括号处切分：
		// `[:lower:]` → `[` `:lower:` `]`，`{o[k]}` → 6 词）
		if c == '[' || c == ']' {
			bStart := p.L.b
			advance(p.L)
			parts = append(parts, mk(p, "word", bStart, p.L.b, nil))
			continue
		}
		// 裸词片段
		frag := parseBareWord(p)
		if frag == nil {
			break
		}
		// `NN#${...}` / `NN#$(...)` → (number (expansion|command_substitution))
		// grammar：number 可为 seq(/-?(0x)?[0-9]+#/, choice(expansion,cmd_sub))。
		// `10#${cmd}` 不是 concatenation——单 number 节点带展开子节点。
		// frag 以 # 结尾且下一个是 $ {/( 时在此检出
		if frag.Type == "word" && endsWithBaseMarker(frag.Text) &&
			peek(p.L, 0) == '$' &&
			(peek(p.L, 1) == '{' || peek(p.L, 1) == '(') {
			exp := parseDollarLike(p)
			if exp != nil {
				// 前缀 NN# 是 grammar 匿名 pattern——仅展开/cmd_sub 为具名子节点
				parts = append(parts, mk(p, "number", frag.StartIndex,
					exp.EndIndex, []*TsNode{exp}))
				continue
			}
		}
		parts = append(parts, frag)
	}
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		return parts[0]
	}
	first := parts[0]
	last := parts[len(parts)-1]
	return mk(p, "concatenation", first.StartIndex, last.EndIndex, parts)
}

// endsWithBaseMarker 对齐正则 /^-?(0x)?[0-9]+#$/
func endsWithBaseMarker(s string) bool {
	if len(s) == 0 || s[len(s)-1] != '#' {
		return false
	}
	body := s[:len(s)-1]
	body = stringsTrimPrefix(body, "0x")
	body = stringsTrimPrefix(body, "-")
	if body == "" {
		return false
	}
	for _, r := range body {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func stringsTrimPrefix(s, prefix string) string {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}

// parseBareWord 裸词片段（对齐 parseBareWord）
func parseBareWord(p *ParseState) *TsNode {
	start := p.L.b
	startI := p.L.i
	for p.L.i < p.L.len {
		c := peek(p.L, 0)
		if c == '\\' {
			if p.L.i+1 >= p.L.len {
				// 真 EOF 前孤立 \——tree-sitter 产出不含 \ 的词 + 兄弟
				// ERROR 节点。在此停止；调用方产出 ERROR
				break
			}
			nx := p.L.runes[p.L.i+1]
			if nx == '\n' || (nx == '\r' && p.L.i+2 < p.L.len && p.L.runes[p.L.i+2] == '\n') {
				// 行续接断词（tree-sitter 怪癖）——处理 \r?\n
				break
			}
			advance(p.L)
			advance(p.L)
			continue
		}
		if c == 0 || c == ' ' || c == '\t' || c == '\n' || c == '\r' ||
			c == '|' || c == '&' || c == ';' || c == '(' || c == ')' ||
			c == '<' || c == '>' || c == '"' || c == '\'' || c == '$' ||
			c == '`' || c == '{' || c == '}' || c == '[' || c == ']' {
			break
		}
		advance(p.L)
	}
	if p.L.b == start {
		return nil
	}
	text := string(p.srcRunes[startI:p.L.i])
	typ := "word"
	if isAllDigitsSigned(text) {
		typ = "number"
	}
	return mk(p, typ, start, p.L.b, nil)
}
