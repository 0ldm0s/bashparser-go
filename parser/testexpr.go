package parser

// testexpr.go：test 命令与 test 表达式（对齐 bashParser.ts 1081-1116 的
// parseTestCommand 与 3699-4069 行的 test 表达式家族）。

// parseTestCommand `[ expr ]` / `[[ expr ]]` 命令（对齐 parseCommand 的
// test 分支 1081-1116 行）
func parseTestCommand(p *ParseState, t Token) *TsNode {
	open := leaf(p, t.Value, t)
	closer := "]"
	if t.Value == "[[" {
		closer = "]]"
	}
	// grammar：`[` 内容可为 choice(_expression, redirected_statement)。
	// 先试表达式；未到达 `]` 则回溯按 redirected_statement 解析
	//（处理 `[ ! cmd -v go &>/dev/null ]`）
	exprSave := saveLex(p.L)
	expr := parseTestExpr(p, closer)
	skipBlanks(p.L)
	if t.Value == "[" && peek(p.L, 0) != ']' {
		// 表达式解析未到 `]`——按 redirected_statement 重试。穿入 `]`
		// 停止 token 使 parseSimpleCommand 不把它当参数吃掉
		restoreLex(p.L, exprSave)
		prevStop := p.stopToken
		p.stopToken = "]"
		rstmt := parseCommand(p)
		p.stopToken = prevStop
		if rstmt != nil && rstmt.Type == "redirected_statement" {
			expr = rstmt
		} else {
			// 都不行——恢复并保留表达式结果
			restoreLex(p.L, exprSave)
			expr = parseTestExpr(p, closer)
		}
		skipBlanks(p.L)
	}
	closeTok := NextToken(p.L, CtxArg)
	var close *TsNode
	if closeTok.Value == closer {
		close = leaf(p, closer, closeTok)
	} else {
		close = mk(p, closer, open.EndIndex, open.EndIndex, nil)
	}
	kids := []*TsNode{open, close}
	if expr != nil {
		kids = []*TsNode{open, expr, close}
	}
	return mk(p, "test_command", open.StartIndex, close.EndIndex, kids)
}

// parseTestExpr test 表达式入口（对齐 parseTestExpr）
func parseTestExpr(p *ParseState, closer string) *TsNode {
	return parseTestOr(p, closer)
}

// parseTestOr || 层（对齐 parseTestOr）
func parseTestOr(p *ParseState, closer string) *TsNode {
	left := parseTestAnd(p, closer)
	if left == nil {
		return nil
	}
	for {
		skipBlanks(p.L)
		save := saveLex(p.L)
		if peek(p.L, 0) == '|' && peek(p.L, 1) == '|' {
			s := p.L.b
			advance(p.L)
			advance(p.L)
			op := mk(p, "||", s, p.L.b, nil)
			right := parseTestAnd(p, closer)
			if right == nil {
				restoreLex(p.L, save)
				break
			}
			left = mk(p, "binary_expression", left.StartIndex, right.EndIndex,
				[]*TsNode{left, op, right})
		} else {
			break
		}
	}
	return left
}

// parseTestAnd && 层（对齐 parseTestAnd）
func parseTestAnd(p *ParseState, closer string) *TsNode {
	left := parseTestUnary(p, closer)
	if left == nil {
		return nil
	}
	for {
		skipBlanks(p.L)
		if peek(p.L, 0) == '&' && peek(p.L, 1) == '&' {
			s := p.L.b
			advance(p.L)
			advance(p.L)
			op := mk(p, "&&", s, p.L.b, nil)
			right := parseTestUnary(p, closer)
			if right == nil {
				break
			}
			left = mk(p, "binary_expression", left.StartIndex, right.EndIndex,
				[]*TsNode{left, op, right})
		} else {
			break
		}
	}
	return left
}

// parseTestUnary 括号层（对齐 parseTestUnary）
func parseTestUnary(p *ParseState, closer string) *TsNode {
	skipBlanks(p.L)
	c := peek(p.L, 0)
	if c == '(' {
		s := p.L.b
		advance(p.L)
		open := mk(p, "(", s, p.L.b, nil)
		inner := parseTestOr(p, closer)
		skipBlanks(p.L)
		var close *TsNode
		if peek(p.L, 0) == ')' {
			cs := p.L.b
			advance(p.L)
			close = mk(p, ")", cs, p.L.b, nil)
		} else {
			close = mk(p, ")", p.L.b, p.L.b, nil)
		}
		kids := []*TsNode{open, close}
		if inner != nil {
			kids = []*TsNode{open, inner, close}
		}
		return mk(p, "parenthesized_expression", open.StartIndex,
			close.EndIndex, kids)
	}
	return parseTestBinary(p, closer)
}

// parseTestNegatablePrimary 可取反的 test 主元（对齐 parseTestNegatablePrimary）：
// `!` 取反、test 操作符（-f）、或括号主元——不是二元比较。用作
// binary_expression 的 LHS，使 `! x =~ y` 的 `!` 只绑 `x`
func parseTestNegatablePrimary(p *ParseState, closer string) *TsNode {
	skipBlanks(p.L)
	c := peek(p.L, 0)
	if c == '!' {
		s := p.L.b
		advance(p.L)
		bang := mk(p, "!", s, p.L.b, nil)
		inner := parseTestNegatablePrimary(p, closer)
		if inner == nil {
			return bang
		}
		return mk(p, "unary_expression", bang.StartIndex, inner.EndIndex,
			[]*TsNode{bang, inner})
	}
	if c == '-' && isIdentStart(peek(p.L, 1)) {
		s := p.L.b
		advance(p.L)
		for isIdentChar(peek(p.L, 0)) {
			advance(p.L)
		}
		op := mk(p, "test_operator", s, p.L.b, nil)
		skipBlanks(p.L)
		arg := parseTestPrimary(p, closer)
		if arg == nil {
			return op
		}
		return mk(p, "unary_expression", op.StartIndex, arg.EndIndex,
			[]*TsNode{op, arg})
	}
	return parseTestPrimary(p, closer)
}

// parseTestBinary 二元比较（对齐 parseTestBinary）：
// == != =~ = < > 与 -eq -lt 等 test_operator。
// `[[ ]]` 内 ==/!=/=/=~ 的 RHS 有专用模式解析（括号计数使 @(a|b|c)
// 不被 | 破坏；分段成为 extglob_pattern/regex）
func parseTestBinary(p *ParseState, closer string) *TsNode {
	skipBlanks(p.L)
	// test 语境中 `!` 绑定紧于 =~/==：
	// `[[ ! "x" =~ y ]]` → (binary_expression (unary_expression (string)) (regex))
	// `[[ ! -f x ]]` → (unary_expression ! (unary_expression (test_operator) (word)))
	left := parseTestNegatablePrimary(p, closer)
	if left == nil {
		return nil
	}
	skipBlanks(p.L)
	// 二元比较操作符
	c := peek(p.L, 0)
	c1 := peek(p.L, 1)
	os := p.L.b
	var op *TsNode
	switch {
	case c == '=' && c1 == '=':
		advance(p.L)
		advance(p.L)
		op = mk(p, "==", os, p.L.b, nil)
	case c == '!' && c1 == '=':
		advance(p.L)
		advance(p.L)
		op = mk(p, "!=", os, p.L.b, nil)
	case c == '=' && c1 == '~':
		advance(p.L)
		advance(p.L)
		op = mk(p, "=~", os, p.L.b, nil)
	case c == '=' && c1 != '=':
		advance(p.L)
		op = mk(p, "=", os, p.L.b, nil)
	case c == '<' && c1 != '<':
		advance(p.L)
		op = mk(p, "<", os, p.L.b, nil)
	case c == '>' && c1 != '>':
		advance(p.L)
		op = mk(p, ">", os, p.L.b, nil)
	case c == '-' && isIdentStart(c1):
		advance(p.L)
		for isIdentChar(peek(p.L, 0)) {
			advance(p.L)
		}
		op = mk(p, "test_operator", os, p.L.b, nil)
	}
	if op == nil {
		return left
	}
	skipBlanks(p.L)
	// [[ ]] 内 ==/!=/=/=~ 的 RHS 特殊模式解析
	if closer == "]]" {
		switch op.Type {
		case "=~":
			skipBlanks(p.L)
			// 整个 RHS 为引号串时产出 string/raw_string 而非 regex：
			// `[[ "$x" =~ "$y" ]]` → (binary_expression (string)(string))。
			// 引号后有内容（`' boop '(.*)$`）时整个 RHS 保持单 (regex)。
			// 前瞻引号之后检查
			rc := peek(p.L, 0)
			var rhs *TsNode
			if rc == '"' || rc == '\'' {
				save := saveLex(p.L)
				var quoted *TsNode
				if rc == '"' {
					quoted = parseDoubleQuoted(p)
				} else {
					quoted = leaf(p, "raw_string", NextToken(p.L, CtxArg))
				}
				// 检查 RHS 是否在此结束：仅空白后为 ]] 或 &&/|| 或换行
				j := p.L.i
				for j < p.L.len && (p.src[j] == ' ' || p.src[j] == '\t') {
					j++
				}
				nc, nc1 := byte(0), byte(0)
				if j < p.L.len {
					nc = p.src[j]
				}
				if j+1 < p.L.len {
					nc1 = p.src[j+1]
				}
				if (nc == ']' && nc1 == ']') || (nc == '&' && nc1 == '&') ||
					(nc == '|' && nc1 == '|') || nc == '\n' || nc == 0 {
					rhs = quoted
				} else {
					restoreLex(p.L, save)
				}
			}
			if rhs == nil {
				rhs = parseTestRegexRhs(p)
			}
			if rhs == nil {
				return left
			}
			return mk(p, "binary_expression", left.StartIndex, rhs.EndIndex,
				[]*TsNode{left, op, rhs})
		case "=":
			// 单 `=` 按 tree-sitter 产出 (regex)
			rhs := parseTestRegexRhs(p)
			if rhs == nil {
				return left
			}
			return mk(p, "binary_expression", left.StartIndex, rhs.EndIndex,
				[]*TsNode{left, op, rhs})
		case "==", "!=":
			parts := parseTestExtglobRhs(p)
			if len(parts) == 0 {
				return left
			}
			last := parts[len(parts)-1]
			kids := append([]*TsNode{left, op}, parts...)
			return mk(p, "binary_expression", left.StartIndex, last.EndIndex, kids)
		}
	}
	right := parseTestPrimary(p, closer)
	if right == nil {
		return left
	}
	return mk(p, "binary_expression", left.StartIndex, right.EndIndex,
		[]*TsNode{left, op, right})
}

// parseTestRegexRhs [[ ]] 内 =~ 的 RHS——扫为单 (regex) 节点，括号/方括号
// 计数使 regex 内的 | ( ) 不破坏解析。停在 ]] 或 空白+&&/||（对齐
// parseTestRegexRhs）
func parseTestRegexRhs(p *ParseState) *TsNode {
	skipBlanks(p.L)
	start := p.L.b
	parenDepth := 0
	bracketDepth := 0
	for p.L.i < p.L.len {
		c := peek(p.L, 0)
		if c == '\\' && p.L.i+1 < p.L.len {
			advance(p.L)
			advance(p.L)
			continue
		}
		if c == '\n' {
			break
		}
		if parenDepth == 0 && bracketDepth == 0 {
			if c == ']' && peek(p.L, 1) == ']' {
				break
			}
			if c == ' ' || c == '\t' {
				// 前瞻空白后的 ]] 或 &&/||
				j := p.L.i
				for j < p.L.len && (p.src[j] == ' ' || p.src[j] == '\t') {
					j++
				}
				nc, nc1 := byte(0), byte(0)
				if j < p.L.len {
					nc = p.src[j]
				}
				if j+1 < p.L.len {
					nc1 = p.src[j+1]
				}
				if (nc == ']' && nc1 == ']') || (nc == '&' && nc1 == '&') ||
					(nc == '|' && nc1 == '|') {
					break
				}
				advance(p.L)
				continue
			}
		}
		if c == '(' {
			parenDepth++
		} else if c == ')' && parenDepth > 0 {
			parenDepth--
		} else if c == '[' {
			bracketDepth++
		} else if c == ']' && bracketDepth > 0 {
			bracketDepth--
		}
		advance(p.L)
	}
	if p.L.b == start {
		return nil
	}
	return mk(p, "regex", start, p.L.b, nil)
}

// parseTestExtglobRhs [[ ]] 内 ==/!=/= 的 RHS——返回片段数组。裸文本 →
// extglob_pattern（@(a|b) 括号计数）；$(...)/${}/引号 → 正确节点类型。
// 多片段按 tree-sitter 成为 binary_expression 的平铺 children（对齐
// parseTestExtglobRhs）
func parseTestExtglobRhs(p *ParseState) []*TsNode {
	skipBlanks(p.L)
	var parts []*TsNode
	segStart := p.L.b
	segStartI := p.L.i
	parenDepth := 0
	flushSeg := func() {
		if p.L.i > segStartI {
			text := string(p.srcRunes[segStartI:p.L.i])
			// 纯数字保持 number；其余是 extglob_pattern
			typ := "extglob_pattern"
			if isAllDigits(text) {
				typ = "number"
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
		if c == '\n' {
			break
		}
		if parenDepth == 0 {
			if c == ']' && peek(p.L, 1) == ']' {
				break
			}
			if c == ' ' || c == '\t' {
				j := p.L.i
				for j < p.L.len && (p.src[j] == ' ' || p.src[j] == '\t') {
					j++
				}
				nc, nc1 := byte(0), byte(0)
				if j < p.L.len {
					nc = p.src[j]
				}
				if j+1 < p.L.len {
					nc1 = p.src[j+1]
				}
				if (nc == ']' && nc1 == ']') || (nc == '&' && nc1 == '&') ||
					(nc == '|' && nc1 == '|') {
					break
				}
				advance(p.L)
				continue
			}
		}
		// $ " ' 即使在 @( ) extglob 括号内也必须解析——parseDollarLike
		// 消费配对的 ) 使 parenDepth 保持一致
		if c == '$' {
			c1 := peek(p.L, 1)
			if c1 == '(' || c1 == '{' || isIdentStart(c1) || specialVars[c1] {
				flushSeg()
				if exp := parseDollarLike(p); exp != nil {
					parts = append(parts, exp)
				}
				segStart = p.L.b
				segStartI = p.L.i
				continue
			}
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
		if c == '(' {
			parenDepth++
		} else if c == ')' && parenDepth > 0 {
			parenDepth--
		}
		advance(p.L)
	}
	flushSeg()
	return parts
}

// parseTestPrimary test 主元（对齐 parseTestPrimary）：词，遇 closer 停
func parseTestPrimary(p *ParseState, closer string) *TsNode {
	skipBlanks(p.L)
	// 停在闭合符
	if closer == "]" && peek(p.L, 0) == ']' {
		return nil
	}
	if closer == "]]" && peek(p.L, 0) == ']' && peek(p.L, 1) == ']' {
		return nil
	}
	return parseWord(p, CtxArg)
}
