package parser

// assignment.go：赋值与下标解析（对齐 bashParser.ts 1406-1562 行）：
// maybeRedirect / tryParseAssignment / parseSubscriptIndexInline /
// parseSubscriptIndex。

// maybeRedirect 复合语句后的尾随重定向包装（对齐 maybeRedirect）。
// allowHerestring=false 时 herestring 终止重定向流（回退词法位置）
func maybeRedirect(p *ParseState, node *TsNode, allowHerestring bool) *TsNode {
	var redirects []*TsNode
	for {
		skipBlanks(p.L)
		save := saveLex(p.L)
		r := tryParseRedirect(p, false)
		if r == nil {
			break
		}
		if r.Type == "herestring_redirect" && !allowHerestring {
			restoreLex(p.L, save)
			break
		}
		redirects = append(redirects, r)
	}
	if len(redirects) == 0 {
		return node
	}
	last := redirects[len(redirects)-1]
	return mk(p, "redirected_statement", node.StartIndex, last.EndIndex,
		append([]*TsNode{node}, redirects...))
}

// tryParseAssignment 赋值解析（对齐 tryParseAssignment）：
// 标识符 [下标] =/= 值；值可为数组 (a b c) 或词
func tryParseAssignment(p *ParseState) *TsNode {
	save := saveLex(p.L)
	skipBlanks(p.L)
	startB := p.L.b
	// 必须以标识符字符开头
	if !isIdentStart(peek(p.L, 0)) {
		restoreLex(p.L, save)
		return nil
	}
	for isIdentChar(peek(p.L, 0)) {
		advance(p.L)
	}
	nameEnd := p.L.b
	// 可选下标
	subEnd := nameEnd
	if peek(p.L, 0) == '[' {
		advance(p.L)
		depth := 1
		for p.L.i < p.L.len && depth > 0 {
			c := peek(p.L, 0)
			if c == '[' {
				depth++
			} else if c == ']' {
				depth--
			}
			advance(p.L)
		}
		subEnd = p.L.b
	}
	c := peek(p.L, 0)
	c1 := peek(p.L, 1)
	var op string
	if c == '=' && c1 != '=' {
		op = "="
	} else if c == '+' && c1 == '=' {
		op = "+="
	} else {
		restoreLex(p.L, save)
		return nil
	}
	nameNode := mk(p, "variable_name", startB, nameEnd, nil)
	// 下标包装 subscript 节点
	lhs := nameNode
	if subEnd > nameEnd {
		brOpen := mk(p, "[", nameEnd, nameEnd+1, nil)
		idx := parseSubscriptIndex(p, nameEnd+1, subEnd-1)
		brClose := mk(p, "]", subEnd-1, subEnd, nil)
		lhs = mk(p, "subscript", startB, subEnd,
			[]*TsNode{nameNode, brOpen, idx, brClose})
	}
	opStart := p.L.b
	advance(p.L)
	if op == "+=" {
		advance(p.L)
	}
	opEnd := p.L.b
	opNode := mk(p, op, opStart, opEnd, nil)
	var val *TsNode
	if peek(p.L, 0) == '(' {
		// 数组
		aoTok := NextToken(p.L, CtxCmd)
		aOpen := leaf(p, "(", aoTok)
		elems := []*TsNode{aOpen}
		for {
			skipBlanks(p.L)
			if peek(p.L, 0) == ')' {
				break
			}
			e := parseWord(p, CtxArg)
			if e == nil {
				break
			}
			elems = append(elems, e)
		}
		acTok := NextToken(p.L, CtxCmd)
		var aClose *TsNode
		if acTok.Value == ")" {
			aClose = leaf(p, ")", acTok)
		} else {
			aClose = mk(p, ")", aOpen.EndIndex, aOpen.EndIndex, nil)
		}
		elems = append(elems, aClose)
		val = mk(p, "array", aOpen.StartIndex, aClose.EndIndex, elems)
	} else {
		c2 := peek(p.L, 0)
		if c2 != 0 && c2 != ' ' && c2 != '\t' && c2 != '\n' &&
			c2 != ';' && c2 != '&' && c2 != '|' && c2 != ')' && c2 != '}' {
			val = parseWord(p, CtxArg)
		}
	}
	kids := []*TsNode{lhs, opNode}
	end := opEnd
	if val != nil {
		kids = append(kids, val)
		end = val.EndIndex
	}
	return mk(p, "variable_assignment", startB, end, kids)
}

// parseSubscriptIndexInline 下标内容按算术解析（对齐 parseSubscriptIndexInline）：
// `${a[1+2]}` → binary_expression；`${a[++i]}` → unary_expression(word)；
// `${a[(($n+1))]}` → compound_statement。@/* 等简单形态回退 word
func parseSubscriptIndexInline(p *ParseState) *TsNode {
	skipBlanks(p.L)
	c := peek(p.L, 0)
	// @ 或 * 单独 → word（关联数组全键）
	if (c == '@' || c == '*') && peek(p.L, 1) == ']' {
		s := p.L.b
		advance(p.L)
		return mk(p, "word", s, p.L.b, nil)
	}
	// ((expr)) → compound_statement 包内层算术
	if c == '(' && peek(p.L, 1) == '(' {
		oStart := p.L.b
		advance(p.L)
		advance(p.L)
		open := mk(p, "((", oStart, p.L.b, nil)
		inner := parseArithExpr(p, "))", "var")
		skipBlanks(p.L)
		var close *TsNode
		if peek(p.L, 0) == ')' && peek(p.L, 1) == ')' {
			cs := p.L.b
			advance(p.L)
			advance(p.L)
			close = mk(p, "))", cs, p.L.b, nil)
		} else {
			close = mk(p, "))", p.L.b, p.L.b, nil)
		}
		kids := []*TsNode{open, close}
		if inner != nil {
			kids = []*TsNode{open, inner, close}
		}
		return mk(p, "compound_statement", open.StartIndex, close.EndIndex, kids)
	}
	// 算术——下标内裸标识符按 'word' 模式（tree-sitter：
	// ${words[++counter]} → unary_expression(word)）
	return parseArithExpr(p, "]", "word")
}

// parseSubscriptIndex 遗留字节区间下标解析（对齐 parseSubscriptIndex；
// 供预扫描调用方使用）
func parseSubscriptIndex(p *ParseState, startB, endB int) *TsNode {
	text := sliceBytes(p, startB, endB)
	if isAllDigits(text) {
		return mk(p, "number", startB, endB, nil)
	}
	if len(text) >= 2 && text[0] == '$' && isIdentStart(rune(text[1])) {
		allIdent := true
		for _, r := range text[1:] {
			if !isIdentChar(r) {
				allIdent = false
				break
			}
		}
		if allIdent {
			dollar := mk(p, "$", startB, startB+1, nil)
			vn := mk(p, "variable_name", startB+1, endB, nil)
			return mk(p, "simple_expansion", startB, endB, []*TsNode{dollar, vn})
		}
	}
	if len(text) == 2 && text[0] == '$' && specialVars[rune(text[1])] {
		dollar := mk(p, "$", startB, startB+1, nil)
		vn := mk(p, "special_variable_name", startB+1, endB, nil)
		return mk(p, "simple_expansion", startB, endB, []*TsNode{dollar, vn})
	}
	return mk(p, "word", startB, endB, nil)
}
