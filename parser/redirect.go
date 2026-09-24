package parser

// redirect.go：重定向解析（对齐 bashParser.ts 1588-1860 行）：
// isRedirectLiteralStart / tryParseRedirect / parseProcessSub。

// isRedirectLiteralStart 当前位置能否开始重定向目标字面量（对齐同名）。
// 在重定向操作符/终止符/fd 前缀操作符处返回 false——file_redirect 的
// repeat1($._literal) 才能在正确边界停住
func isRedirectLiteralStart(p *ParseState) bool {
	c := peek(p.L, 0)
	if c == 0 || c == '\n' {
		return false
	}
	if c == '|' || c == '&' || c == ';' || c == '(' || c == ')' {
		return false
	}
	// 重定向操作符；<( >( 是进程替换——那些是字面量
	if c == '<' || c == '>' {
		return peek(p.L, 1) == '('
	}
	// N< N> fd 前缀——开始新重定向而非字面量
	if isDigit(c) {
		j := p.L.i
		for j < p.L.len && isDigit(p.L.runes[j]) {
			j++
		}
		after := rune(0)
		if j < p.L.len {
			after = p.L.runes[j]
		}
		if after == '>' || after == '<' {
			return false
		}
	}
	// `}` 在 file_redirect 眼里是词字符（`>$HOME}` 合法），但顶层它
	// 终止 compound_statement——需要停
	if c == '}' {
		return false
	}
	// test 命令闭合——`[` 上下文经 parseSimpleCommand 调来时，
	// `]` 必须终止以便 parseCommand 返回后 `[` 处理器消费它
	if p.stopToken == "]" && c == ']' {
		return false
	}
	return true
}

// tryParseRedirect 解析重定向操作符 + 目标（对齐 tryParseRedirect）。
// greedy=true：file_redirect 按 grammar prec.left 贪心消费字面量
// （`cmd >f a b c` 的 a b c 归重定向不归命令）；greedy=false
// （preRedirect 语境）只取 1 个目标——command 的动态优先级压过
// redirected_statement 的 prec(-1)
func tryParseRedirect(p *ParseState, greedy bool) *TsNode {
	save := saveLex(p.L)
	skipBlanks(p.L)
	// fd 前缀？
	var fd *TsNode
	if isDigit(peek(p.L, 0)) {
		startB := p.L.b
		j := p.L.i
		for j < p.L.len && isDigit(p.L.runes[j]) {
			j++
		}
		after := rune(0)
		if j < p.L.len {
			after = p.L.runes[j]
		}
		if after == '>' || after == '<' {
			for p.L.i < j {
				advance(p.L)
			}
			fd = mk(p, "file_descriptor", startB, p.L.b, nil)
		}
	}
	t := NextToken(p.L, CtxArg)
	if t.Type != TokOp {
		restoreLex(p.L, save)
		return nil
	}
	v := t.Value
	if v == "<<<" {
		op := leaf(p, "<<<", t)
		skipBlanks(p.L)
		target := parseWord(p, CtxArg)
		end := op.EndIndex
		kids := []*TsNode{op}
		if target != nil {
			end = target.EndIndex
			kids = append(kids, target)
		}
		startIdx := op.StartIndex
		if fd != nil {
			startIdx = fd.StartIndex
			kids = append([]*TsNode{fd}, kids...)
		}
		return mk(p, "herestring_redirect", startIdx, end, kids)
	}
	if v == "<<" || v == "<<-" {
		return parseHeredocRedirect(p, t, v, fd)
	}
	// 关闭 fd 变体：`<&-` `>&-` 目标可选（0 或 1 个）
	if v == "<&-" || v == ">&-" {
		op := leaf(p, v, t)
		kids := []*TsNode{}
		if fd != nil {
			kids = append(kids, fd)
		}
		kids = append(kids, op)
		// 可选单目标——仅当下一个是字面量才消费
		skipBlanks(p.L)
		dSave := saveLex(p.L)
		var dest *TsNode
		if isRedirectLiteralStart(p) {
			dest = parseWord(p, CtxArg)
		}
		if dest != nil {
			kids = append(kids, dest)
		} else {
			restoreLex(p.L, dSave)
		}
		startIdx := op.StartIndex
		if fd != nil {
			startIdx = fd.StartIndex
		}
		end := op.EndIndex
		if dest != nil {
			end = dest.EndIndex
		}
		return mk(p, "file_redirect", startIdx, end, kids)
	}
	switch v {
	case ">", ">>", ">&", ">|", "&>", "&>>", "<", "<&":
		op := leaf(p, v, t)
		kids := []*TsNode{}
		if fd != nil {
			kids = append(kids, fd)
		}
		kids = append(kids, op)
		// grammar：目标 repeat1($._literal)——贪心消费字面量至非字面量。
		// prec.left 使 `cmd >f a b c` 的 a b c 归 file_redirect 不归
		// command——结构性怪癖，语料一致性所需。preRedirect 语境只取 1 个
		end := op.EndIndex
		taken := 0
		for {
			skipBlanks(p.L)
			if !isRedirectLiteralStart(p) {
				break
			}
			if !greedy && taken >= 1 {
				break
			}
			tc := peek(p.L, 0)
			tc1 := peek(p.L, 1)
			var target *TsNode
			if (tc == '<' || tc == '>') && tc1 == '(' {
				target = parseProcessSub(p)
			} else {
				target = parseWord(p, CtxArg)
			}
			if target == nil {
				break
			}
			kids = append(kids, target)
			end = target.EndIndex
			taken++
		}
		startIdx := op.StartIndex
		if fd != nil {
			startIdx = fd.StartIndex
		}
		return mk(p, "file_redirect", startIdx, end, kids)
	}
	restoreLex(p.L, save)
	return nil
}

// parseProcessSub 进程替换 <(cmd) / >(cmd)（对齐 parseProcessSub）
func parseProcessSub(p *ParseState) *TsNode {
	c := peek(p.L, 0)
	if (c != '<' && c != '>') || peek(p.L, 1) != '(' {
		return nil
	}
	start := p.L.b
	advance(p.L)
	advance(p.L)
	open := mk(p, string(c)+"(", start, p.L.b, nil)
	body := parseStatements(p, ")")
	skipBlanks(p.L)
	var close *TsNode
	if peek(p.L, 0) == ')' {
		cs := p.L.b
		advance(p.L)
		close = mk(p, ")", cs, p.L.b, nil)
	} else {
		close = mk(p, ")", p.L.b, p.L.b, nil)
	}
	return mk(p, "process_substitution", start, close.EndIndex,
		append([]*TsNode{open}, append(body, close)...))
}
