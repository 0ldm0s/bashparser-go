package parser

// control.go：控制结构 I（对齐 bashParser.ts 3152-3440 行）：
// parseIf / parseWhile / parseFor / parseDoGroup / parseCase / parseCaseItem。

// parseIf if 语句（对齐 parseIf）
func parseIf(p *ParseState, ifTok Token) *TsNode {
	ifKw := leaf(p, "if", ifTok)
	kids := []*TsNode{ifKw}
	cond := parseStatements(p, "")
	kids = append(kids, cond...)
	consumeKeyword(p, "then", &kids)
	body := parseStatements(p, "")
	kids = append(kids, body...)
	for {
		save := saveLex(p.L)
		t := NextToken(p.L, CtxCmd)
		if t.Type == TokWord && t.Value == "elif" {
			eKw := leaf(p, "elif", t)
			eCond := parseStatements(p, "")
			eKids := append([]*TsNode{eKw}, eCond...)
			consumeKeyword(p, "then", &eKids)
			eBody := parseStatements(p, "")
			eKids = append(eKids, eBody...)
			last := eKids[len(eKids)-1]
			kids = append(kids, mk(p, "elif_clause", eKw.StartIndex, last.EndIndex, eKids))
		} else if t.Type == TokWord && t.Value == "else" {
			elKw := leaf(p, "else", t)
			elBody := parseStatements(p, "")
			last := elKw
			if n := len(elBody); n > 0 {
				last = elBody[n-1]
			}
			kids = append(kids, mk(p, "else_clause", elKw.StartIndex, last.EndIndex,
				append([]*TsNode{elKw}, elBody...)))
		} else {
			restoreLex(p.L, save)
			break
		}
	}
	consumeKeyword(p, "fi", &kids)
	last := kids[len(kids)-1]
	return mk(p, "if_statement", ifKw.StartIndex, last.EndIndex, kids)
}

// parseWhile while / until 语句（对齐 parseWhile）
func parseWhile(p *ParseState, kwTok Token) *TsNode {
	kw := leaf(p, kwTok.Value, kwTok)
	kids := []*TsNode{kw}
	cond := parseStatements(p, "")
	kids = append(kids, cond...)
	if dg := parseDoGroup(p); dg != nil {
		kids = append(kids, dg)
	}
	last := kids[len(kids)-1]
	return mk(p, "while_statement", kw.StartIndex, last.EndIndex, kids)
}

// parseFor for / select 语句（对齐 parseFor）
func parseFor(p *ParseState, forTok Token) *TsNode {
	forKw := leaf(p, forTok.Value, forTok)
	skipBlanks(p.L)
	// C 风格 for (( ; ; ))——仅 `for`，不含 select
	if forTok.Value == "for" && peek(p.L, 0) == '(' && peek(p.L, 1) == '(' {
		oStart := p.L.b
		advance(p.L)
		advance(p.L)
		open := mk(p, "((", oStart, p.L.b, nil)
		kids := []*TsNode{forKw, open}
		// init; cond; update——三者都用 'assign' 模式：`c = expr` 产出
		// variable_assignment，裸标识符（`c<=5` 的 c）→ word。每个子句
		// 可为逗号分隔列表
		for k := 0; k < 3; k++ {
			skipBlanks(p.L)
			stop := ";"
			if k >= 2 {
				stop = "))"
			}
			es := parseArithCommaList(p, stop, "assign")
			kids = append(kids, es...)
			if k < 2 {
				if peek(p.L, 0) == ';' {
					s := p.L.b
					advance(p.L)
					kids = append(kids, mk(p, ";", s, p.L.b, nil))
				}
			}
		}
		skipBlanks(p.L)
		if peek(p.L, 0) == ')' && peek(p.L, 1) == ')' {
			cStart := p.L.b
			advance(p.L)
			advance(p.L)
			kids = append(kids, mk(p, "))", cStart, p.L.b, nil))
		}
		// 可选 ; 或换行
		save := saveLex(p.L)
		sep := NextToken(p.L, CtxCmd)
		if sep.Type == TokOp && sep.Value == ";" {
			kids = append(kids, leaf(p, ";", sep))
		} else if sep.Type != TokNewline {
			restoreLex(p.L, save)
		}
		if dg := parseDoGroup(p); dg != nil {
			kids = append(kids, dg)
		} else {
			// C 风格 for 也可用 `{ ... }` 体代替 do...done
			skipNewlines(p)
			skipBlanks(p.L)
			if peek(p.L, 0) == '{' {
				bOpen := p.L.b
				advance(p.L)
				brace := mk(p, "{", bOpen, p.L.b, nil)
				body := parseStatements(p, "}")
				var bClose *TsNode
				if peek(p.L, 0) == '}' {
					cs := p.L.b
					advance(p.L)
					bClose = mk(p, "}", cs, p.L.b, nil)
				} else {
					bClose = mk(p, "}", p.L.b, p.L.b, nil)
				}
				kids = append(kids, mk(p, "compound_statement", brace.StartIndex,
					bClose.EndIndex, append(append([]*TsNode{brace}, body...), bClose)))
			}
		}
		last := kids[len(kids)-1]
		return mk(p, "c_style_for_statement", forKw.StartIndex, last.EndIndex, kids)
	}
	// 常规 for VAR in words; do ... done
	kids := []*TsNode{forKw}
	varTok := NextToken(p.L, CtxArg)
	kids = append(kids, mk(p, "variable_name", varTok.Start, varTok.End, nil))
	skipBlanks(p.L)
	save := saveLex(p.L)
	inTok := NextToken(p.L, CtxArg)
	if inTok.Type == TokWord && inTok.Value == "in" {
		kids = append(kids, leaf(p, "in", inTok))
		for {
			skipBlanks(p.L)
			c := peek(p.L, 0)
			if c == ';' || c == '\n' || c == 0 {
				break
			}
			w := parseWord(p, CtxArg)
			if w == nil {
				break
			}
			kids = append(kids, w)
		}
	} else {
		restoreLex(p.L, save)
	}
	// 分隔符
	save2 := saveLex(p.L)
	sep := NextToken(p.L, CtxCmd)
	if sep.Type == TokOp && sep.Value == ";" {
		kids = append(kids, leaf(p, ";", sep))
	} else if sep.Type != TokNewline {
		restoreLex(p.L, save2)
	}
	if dg := parseDoGroup(p); dg != nil {
		kids = append(kids, dg)
	}
	last := kids[len(kids)-1]
	return mk(p, "for_statement", forKw.StartIndex, last.EndIndex, kids)
}

// parseDoGroup do ... done 组（对齐 parseDoGroup）
func parseDoGroup(p *ParseState) *TsNode {
	skipNewlines(p)
	save := saveLex(p.L)
	doTok := NextToken(p.L, CtxCmd)
	if doTok.Type != TokWord || doTok.Value != "do" {
		restoreLex(p.L, save)
		return nil
	}
	doKw := leaf(p, "do", doTok)
	body := parseStatements(p, "")
	kids := append([]*TsNode{doKw}, body...)
	consumeKeyword(p, "done", &kids)
	last := kids[len(kids)-1]
	return mk(p, "do_group", doKw.StartIndex, last.EndIndex, kids)
}

// parseCase case 语句（对齐 parseCase）
func parseCase(p *ParseState, caseTok Token) *TsNode {
	caseKw := leaf(p, "case", caseTok)
	kids := []*TsNode{caseKw}
	skipBlanks(p.L)
	if word := parseWord(p, CtxArg); word != nil {
		kids = append(kids, word)
	}
	skipBlanks(p.L)
	consumeKeyword(p, "in", &kids)
	skipNewlines(p)
	for {
		skipBlanks(p.L)
		skipNewlines(p)
		save := saveLex(p.L)
		t := NextToken(p.L, CtxArg)
		if t.Type == TokWord && t.Value == "esac" {
			kids = append(kids, leaf(p, "esac", t))
			break
		}
		if t.Type == TokEOF {
			break
		}
		restoreLex(p.L, save)
		item := parseCaseItem(p)
		if item == nil {
			break
		}
		kids = append(kids, item)
	}
	last := kids[len(kids)-1]
	return mk(p, "case_statement", caseKw.StartIndex, last.EndIndex, kids)
}

// parseCaseItem case 分支项（对齐 parseCaseItem）
func parseCaseItem(p *ParseState) *TsNode {
	skipBlanks(p.L)
	start := p.L.b
	var kids []*TsNode
	// 模式前可选 '('——bash 允许 (pattern) 语法
	if peek(p.L, 0) == '(' {
		s := p.L.b
		advance(p.L)
		kids = append(kids, mk(p, "(", s, p.L.b, nil))
	}
	// 模式（可多个 | 分隔）
	isFirstAlt := true
	for {
		skipBlanks(p.L)
		c := peek(p.L, 0)
		if c == ')' || c == 0 {
			break
		}
		pats := parseCasePattern(p)
		if len(pats) == 0 {
			break
		}
		// tree-sitter 怪癖：首个候选带引号时内联为平铺兄弟；后续候选包
		// (concatenation) 且裸段用 `word` 而非 `extglob_pattern`
		if !isFirstAlt && len(pats) > 1 {
			rewritten := make([]*TsNode, len(pats))
			for i, pt := range pats {
				if pt.Type == "extglob_pattern" {
					rewritten[i] = mk(p, "word", pt.StartIndex, pt.EndIndex, nil)
				} else {
					rewritten[i] = pt
				}
			}
			first := rewritten[0]
			last := rewritten[len(rewritten)-1]
			kids = append(kids, mk(p, "concatenation", first.StartIndex,
				last.EndIndex, rewritten))
		} else {
			kids = append(kids, pats...)
		}
		isFirstAlt = false
		skipBlanks(p.L)
		// 候选间的 \<newline> 续行
		if peek(p.L, 0) == '\\' && peek(p.L, 1) == '\n' {
			advance(p.L)
			advance(p.L)
			skipBlanks(p.L)
		}
		if peek(p.L, 0) == '|' {
			s := p.L.b
			advance(p.L)
			kids = append(kids, mk(p, "|", s, p.L.b, nil))
			// | 后的 \<newline> 同为续行
			if peek(p.L, 0) == '\\' && peek(p.L, 1) == '\n' {
				advance(p.L)
				advance(p.L)
			}
		} else {
			break
		}
	}
	if peek(p.L, 0) == ')' {
		s := p.L.b
		advance(p.L)
		kids = append(kids, mk(p, ")", s, p.L.b, nil))
	}
	body := parseStatements(p, "")
	kids = append(kids, body...)
	save := saveLex(p.L)
	term := NextToken(p.L, CtxCmd)
	if term.Type == TokOp &&
		(term.Value == ";;" || term.Value == ";&" || term.Value == ";;&") {
		kids = append(kids, leaf(p, term.Value, term))
	} else {
		restoreLex(p.L, save)
	}
	if len(kids) == 0 {
		return nil
	}
	// tree-sitter 怪癖：体为空且单一模式匹配 extglob 操作符字符前缀
	//（无实际 glob 元字符）时降级为 word。`-o) owner=$2 ;;`（有体）→
	// extglob_pattern；`-g) ;;`（空体）→ word
	if len(body) == 0 {
		for i, k := range kids {
			if k.Type != "extglob_pattern" {
				continue
			}
			text := sliceBytes(p, k.StartIndex, k.EndIndex)
			if isExtglobOpIdentPrefix(text) && !containsGlobMeta(text) {
				kids[i] = mk(p, "word", k.StartIndex, k.EndIndex, nil)
			}
		}
	}
	last := kids[len(kids)-1]
	return mk(p, "case_item", start, last.EndIndex, kids)
}

// isExtglobOpIdentPrefix 对齐正则 /^[-+?*@!][a-zA-Z]/
func isExtglobOpIdentPrefix(s string) bool {
	if len(s) < 2 {
		return false
	}
	first := rune(s[0])
	if first != '-' && first != '+' && first != '?' && first != '*' &&
		first != '@' && first != '!' {
		return false
	}
	c := rune(s[1])
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// containsGlobMeta 对齐正则 /[*?(]/（是否含 glob 元字符）
func containsGlobMeta(s string) bool {
	for _, r := range s {
		if r == '*' || r == '?' || r == '(' {
			return true
		}
	}
	return false
}
