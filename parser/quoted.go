package parser

// quoted.go：双引号字符串与 $ 展开（对齐 bashParser.ts 2339-2553 行）。

// parseDoubleQuoted 双引号字符串（对齐 parseDoubleQuoted）。
// 内含 $ 展开/反引号/转义；空白独占段按 tree-sitter extras 优先级省略
// （`" ${x} "` → (string (expansion))——上游注释：有意偏离全保留语义，
// 依赖空白 string_content 的测试需更新 CCReconcile）
func parseDoubleQuoted(p *ParseState) *TsNode {
	qStart := p.L.b
	advance(p.L)
	qEnd := p.L.b
	openQ := mk(p, `"`, qStart, qEnd, nil)
	parts := []*TsNode{openQ}
	contentStart := p.L.b
	contentStartI := p.L.i
	flushContent := func() {
		if p.L.b > contentStart {
			// 仅空白段省略（extras 优先于 string_content，prec -1）
			txt := string(p.srcRunes[contentStartI:p.L.i])
			if !isBlankOnly(txt) {
				parts = append(parts, mk(p, "string_content", contentStart, p.L.b, nil))
			}
		}
	}
	for p.L.i < p.L.len {
		c := peek(p.L, 0)
		if c == '"' {
			break
		}
		if c == '\\' && p.L.i+1 < p.L.len {
			advance(p.L)
			advance(p.L)
			continue
		}
		if c == '\n' {
			// 在换行处切分 string_content
			flushContent()
			advance(p.L)
			contentStart = p.L.b
			contentStartI = p.L.i
			continue
		}
		if c == '$' {
			c1 := peek(p.L, 1)
			if c1 == '(' || c1 == '{' || isIdentStart(c1) ||
				specialVars[c1] || isDigit(c1) {
				flushContent()
				if exp := parseDollarLike(p); exp != nil {
					parts = append(parts, exp)
				}
				contentStart = p.L.b
				contentStartI = p.L.i
				continue
			}
			// 字符串内非结尾裸 $：tree-sitter 产出匿名 '$' token 切分
			// string_content；紧贴闭合 " 的 $ 吸进前一 string_content
			if c1 != '"' && c1 != 0 {
				flushContent()
				dS := p.L.b
				advance(p.L)
				parts = append(parts, mk(p, "$", dS, p.L.b, nil))
				contentStart = p.L.b
				contentStartI = p.L.i
				continue
			}
		}
		if c == '`' {
			flushContent()
			if bt := parseBacktick(p); bt != nil {
				parts = append(parts, bt)
			}
			contentStart = p.L.b
			contentStartI = p.L.i
			continue
		}
		advance(p.L)
	}
	flushContent()
	var close *TsNode
	if peek(p.L, 0) == '"' {
		cStart := p.L.b
		advance(p.L)
		close = mk(p, `"`, cStart, p.L.b, nil)
	} else {
		close = mk(p, `"`, p.L.b, p.L.b, nil)
	}
	parts = append(parts, close)
	return mk(p, "string", qStart, close.EndIndex, parts)
}

// parseDollarLike $ 引导的展开（对齐 parseDollarLike）
func parseDollarLike(p *ParseState) *TsNode {
	c1 := peek(p.L, 1)
	dStart := p.L.b
	if c1 == '(' && peek(p.L, 2) == '(' {
		// $(( 算术 ))
		advance(p.L)
		advance(p.L)
		advance(p.L)
		open := mk(p, "$((", dStart, p.L.b, nil)
		exprs := parseArithCommaList(p, "))", "var")
		skipBlanks(p.L)
		var close *TsNode
		if peek(p.L, 0) == ')' && peek(p.L, 1) == ')' {
			cStart := p.L.b
			advance(p.L)
			advance(p.L)
			close = mk(p, "))", cStart, p.L.b, nil)
		} else {
			close = mk(p, "))", p.L.b, p.L.b, nil)
		}
		return mk(p, "arithmetic_expansion", dStart, close.EndIndex,
			append(append([]*TsNode{open}, exprs...), close))
	}
	if c1 == '[' {
		// $[ 算术 ]——遗留 bash 语法，同 $((...))
		advance(p.L)
		advance(p.L)
		open := mk(p, "$[", dStart, p.L.b, nil)
		exprs := parseArithCommaList(p, "]", "var")
		skipBlanks(p.L)
		var close *TsNode
		if peek(p.L, 0) == ']' {
			cStart := p.L.b
			advance(p.L)
			close = mk(p, "]", cStart, p.L.b, nil)
		} else {
			close = mk(p, "]", p.L.b, p.L.b, nil)
		}
		return mk(p, "arithmetic_expansion", dStart, close.EndIndex,
			append(append([]*TsNode{open}, exprs...), close))
	}
	if c1 == '(' {
		advance(p.L)
		advance(p.L)
		open := mk(p, "$(", dStart, p.L.b, nil)
		body := parseStatements(p, ")")
		skipBlanks(p.L)
		var close *TsNode
		if peek(p.L, 0) == ')' {
			cStart := p.L.b
			advance(p.L)
			close = mk(p, ")", cStart, p.L.b, nil)
		} else {
			close = mk(p, ")", p.L.b, p.L.b, nil)
		}
		// $(< file) 简写：拆开 redirected_statement → 裸 file_redirect
		// tree-sitter 直接产出 (command_substitution (file_redirect (word)))
		if len(body) == 1 && body[0].Type == "redirected_statement" &&
			len(body[0].Children) == 1 && body[0].Children[0].Type == "file_redirect" {
			body = body[0].Children
		}
		return mk(p, "command_substitution", dStart, close.EndIndex,
			append(append([]*TsNode{open}, body...), close))
	}
	if c1 == '{' {
		advance(p.L)
		advance(p.L)
		open := mk(p, "${", dStart, p.L.b, nil)
		inner := parseExpansionBody(p)
		var close *TsNode
		if peek(p.L, 0) == '}' {
			cStart := p.L.b
			advance(p.L)
			close = mk(p, "}", cStart, p.L.b, nil)
		} else {
			close = mk(p, "}", p.L.b, p.L.b, nil)
		}
		return mk(p, "expansion", dStart, close.EndIndex,
			append(append([]*TsNode{open}, inner...), close))
	}
	// 简单展开 $VAR 或 $? $$ $@ 等
	advance(p.L)
	dEnd := p.L.b
	dollar := mk(p, "$", dStart, dEnd, nil)
	nc := peek(p.L, 0)
	// $_ 仅在后面没有更多 ident 字符时是 special_variable_name
	if nc == '_' && !isIdentChar(peek(p.L, 1)) {
		vStart := p.L.b
		advance(p.L)
		vn := mk(p, "special_variable_name", vStart, p.L.b, nil)
		return mk(p, "simple_expansion", dStart, p.L.b, []*TsNode{dollar, vn})
	}
	if isIdentStart(nc) {
		vStart := p.L.b
		for isIdentChar(peek(p.L, 0)) {
			advance(p.L)
		}
		vn := mk(p, "variable_name", vStart, p.L.b, nil)
		return mk(p, "simple_expansion", dStart, p.L.b, []*TsNode{dollar, vn})
	}
	if isDigit(nc) {
		vStart := p.L.b
		advance(p.L)
		vn := mk(p, "variable_name", vStart, p.L.b, nil)
		return mk(p, "simple_expansion", dStart, p.L.b, []*TsNode{dollar, vn})
	}
	if specialVars[nc] {
		vStart := p.L.b
		advance(p.L)
		vn := mk(p, "special_variable_name", vStart, p.L.b, nil)
		return mk(p, "simple_expansion", dStart, p.L.b, []*TsNode{dollar, vn})
	}
	// 裸 $——字面 $ 叶子（tree-sitter 视尾随 $ 为字面量）
	return dollar
}
