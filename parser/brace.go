package parser

// brace.go：brace 表达式与 brace 形切分（对齐 bashParser.ts 2215-2337 行）。

// tryParseBraceExpr {N..M} 序列表达式（对齐 tryParseBraceExpr）：
// N、M 为数字或单字符；混合形态拒绝
func tryParseBraceExpr(p *ParseState) *TsNode {
	save := saveLex(p.L)
	if peek(p.L, 0) != '{' {
		return nil
	}
	oStart := p.L.b
	advance(p.L)
	oEnd := p.L.b
	// 第一段
	p1Start := p.L.b
	for isDigit(peek(p.L, 0)) || isIdentStart(peek(p.L, 0)) {
		advance(p.L)
	}
	p1End := p.L.b
	if p1End == p1Start || peek(p.L, 0) != '.' || peek(p.L, 1) != '.' {
		restoreLex(p.L, save)
		return nil
	}
	dotStart := p.L.b
	advance(p.L)
	advance(p.L)
	dotEnd := p.L.b
	// 第二段
	p2Start := p.L.b
	for isDigit(peek(p.L, 0)) || isIdentStart(peek(p.L, 0)) {
		advance(p.L)
	}
	p2End := p.L.b
	if p2End == p2Start || peek(p.L, 0) != '}' {
		restoreLex(p.L, save)
		return nil
	}
	cStart := p.L.b
	advance(p.L)
	cEnd := p.L.b
	p1Text := sliceBytes(p, p1Start, p1End)
	p2Text := sliceBytes(p, p2Start, p2End)
	p1IsNum := isAllDigits(p1Text)
	p2IsNum := isAllDigits(p2Text)
	// 合法 brace 表达式：同为数字或同为单字符；混合拒绝
	if p1IsNum != p2IsNum {
		restoreLex(p.L, save)
		return nil
	}
	if !p1IsNum && (len(p1Text) != 1 || len(p2Text) != 1) {
		restoreLex(p.L, save)
		return nil
	}
	p1Type := "word"
	if p1IsNum {
		p1Type = "number"
	}
	p2Type := "word"
	if p2IsNum {
		p2Type = "number"
	}
	return mk(p, "brace_expression", oStart, cEnd, []*TsNode{
		mk(p, "{", oStart, oEnd, nil),
		mk(p, p1Type, p1Start, p1End, nil),
		mk(p, "..", dotStart, dotEnd, nil),
		mk(p, p2Type, p2Start, p2End, nil),
		mk(p, "}", cStart, cEnd, nil),
	})
}

// tryParseBraceLikeCat {a,b,c} 或 {} → 按 tree-sitter 方式切为词片段
//（对齐 tryParseBraceLikeCat）
func tryParseBraceLikeCat(p *ParseState) []*TsNode {
	if peek(p.L, 0) != '{' {
		return nil
	}
	oStart := p.L.b
	advance(p.L)
	oEnd := p.L.b
	inner := []*TsNode{mk(p, "word", oStart, oEnd, nil)}
	for p.L.i < p.L.len {
		bc := peek(p.L, 0)
		// 安全：停在命令终止符使 `{foo;rm x` 正确切分
		if bc == '}' || bc == '\n' || bc == ';' || bc == '|' || bc == '&' ||
			bc == ' ' || bc == '\t' || bc == '<' || bc == '>' ||
			bc == '(' || bc == ')' {
			break
		}
		// [ ] 是单字符词：{o[k]} → { o [ k ] }
		if bc == '[' || bc == ']' {
			bStart := p.L.b
			advance(p.L)
			inner = append(inner, mk(p, "word", bStart, p.L.b, nil))
			continue
		}
		midStart := p.L.b
		for p.L.i < p.L.len {
			mc := peek(p.L, 0)
			if mc == '}' || mc == '\n' || mc == ';' || mc == '|' || mc == '&' ||
				mc == ' ' || mc == '\t' || mc == '<' || mc == '>' ||
				mc == '(' || mc == ')' || mc == '[' || mc == ']' {
				break
			}
			advance(p.L)
		}
		midEnd := p.L.b
		if midEnd > midStart {
			midText := sliceBytes(p, midStart, midEnd)
			midType := "word"
			if isAllDigitsSigned(midText) {
				midType = "number"
			}
			inner = append(inner, mk(p, midType, midStart, midEnd, nil))
		} else {
			break
		}
	}
	if peek(p.L, 0) == '}' {
		cStart := p.L.b
		advance(p.L)
		inner = append(inner, mk(p, "word", cStart, p.L.b, nil))
	}
	return inner
}
