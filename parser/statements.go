package parser

// statements.go：语句层解析（对齐 bashParser.ts 706-992 行）：
// parseProgram / parseStatements / parseAndOr / skipNewlines / parsePipeline。

// parseProgram 程序根节点（对齐 parseProgram）
func parseProgram(p *ParseState) *TsNode {
	var children []*TsNode
	// 跳过前导空白与换行——程序起点是首个内容字节
	skipBlanks(p.L)
	for {
		save := saveLex(p.L)
		t := NextToken(p.L, CtxCmd)
		if t.Type == TokNewline {
			skipBlanks(p.L)
			continue
		}
		restoreLex(p.L, save)
		break
	}
	progStart := p.L.b
	for p.L.i < p.L.len {
		save := saveLex(p.L)
		t := NextToken(p.L, CtxCmd)
		if t.Type == TokEOF {
			break
		}
		if t.Type == TokNewline {
			continue
		}
		if t.Type == TokComment {
			children = append(children, leaf(p, "comment", t))
			continue
		}
		restoreLex(p.L, save)
		stmts := parseStatements(p, "")
		children = append(children, stmts...)
		if len(stmts) == 0 {
			// 无法解析——产出 ERROR 并跳过一个 token
			errTok := NextToken(p.L, CtxCmd)
			if errTok.Type == TokEOF {
				break
			}
			// 程序级游离 ;;（case 外的 var=;;）——tree-sitter 静默略过；
			// 保留前导 ; 为 ERROR（安全：粘贴残留物）
			if errTok.Type == TokOp && errTok.Value == ";;" && len(children) > 0 {
				continue
			}
			children = append(children, mk(p, "ERROR", errTok.Start, errTok.End, nil))
		}
	}
	// tree-sitter 把尾随空白计入 program 范围
	progEnd := progStart
	if len(children) > 0 {
		progEnd = p.srcBytes
	}
	return mk(p, "program", progStart, progEnd, children)
}

// isStatementsCloser 语句流闭合 token（对齐 parseStatements 判定）
func isStatementsCloser(t Token) bool {
	if t.Type != TokOp {
		return false
	}
	switch t.Value {
	case ")", "}", ";;", ";&", ";;&", "))", "]]", "]":
		return true
	}
	return false
}

// parseStatements 解析以 ; & 换行分隔的语句序列（对齐 parseStatements）：
// ; 与 & 为平铺叶子（不包 'list'——只有 && || 才包）；遇终止符或 EOF 停。
// terminator 空串表示无终止符（程序级）。
func parseStatements(p *ParseState, terminator string) []*TsNode {
	var out []*TsNode
	for {
		skipBlanks(p.L)
		save := saveLex(p.L)
		t := NextToken(p.L, CtxCmd)
		if t.Type == TokEOF {
			restoreLex(p.L, save)
			break
		}
		if t.Type == TokNewline {
			// 处理待扫描 heredoc 体
			if len(p.L.heredocs) > 0 {
				scanHeredocBodies(p)
			}
			continue
		}
		if t.Type == TokComment {
			out = append(out, leaf(p, "comment", t))
			continue
		}
		if terminator != "" && t.Type == TokOp && t.Value == terminator {
			restoreLex(p.L, save)
			break
		}
		if isStatementsCloser(t) {
			restoreLex(p.L, save)
			break
		}
		if t.Type == TokBacktick && p.inBacktick > 0 {
			restoreLex(p.L, save)
			break
		}
		if t.Type == TokWord && statementBreakKeywords[t.Value] {
			restoreLex(p.L, save)
			break
		}
		restoreLex(p.L, save)
		stmt := parseAndOr(p)
		if stmt == nil {
			break
		}
		out = append(out, stmt)
		// 找分隔符
		skipBlanks(p.L)
		save2 := saveLex(p.L)
		sep := NextToken(p.L, CtxCmd)
		if sep.Type == TokOp && (sep.Value == ";" || sep.Value == "&") {
			// 终止符跟随则产出分隔符后停——有内容时尾随分隔符在
			// 程序级保留；内层保留
			save3 := saveLex(p.L)
			after := NextToken(p.L, CtxCmd)
			restoreLex(p.L, save3)
			out = append(out, leaf(p, sep.Value, sep))
			if after.Type == TokEOF ||
				(after.Type == TokOp && isStatementsCloser(after)) ||
				(after.Type == TokWord && statementBreakKeywords[after.Value]) {
				continue
			}
		} else if sep.Type == TokNewline {
			if len(p.L.heredocs) > 0 {
				scanHeredocBodies(p)
			}
			continue
		} else {
			restoreLex(p.L, save2)
		}
	}
	return out
}

// parseAndOr && || 连接的 pipeline 链（左结合嵌套，对齐 parseAndOr）。
// tree-sitter 怪癖：末尾 pipeline 的尾随重定向把整个 list 包进
// redirected_statement——`a > x && b > y` →
// redirected_statement(list(redirected_statement(a,>x), &&, b), >y)
func parseAndOr(p *ParseState) *TsNode {
	left := parsePipeline(p)
	if left == nil {
		return nil
	}
	for {
		save := saveLex(p.L)
		t := NextToken(p.L, CtxCmd)
		if t.Type == TokOp && (t.Value == "&&" || t.Value == "||") {
			op := leaf(p, t.Value, t)
			skipNewlines(p)
			right := parsePipeline(p)
			if right == nil {
				left = mk(p, "list", left.StartIndex, op.EndIndex,
					[]*TsNode{left, op})
				break
			}
			// right 为 redirected_statement 时提升其重定向包住 list
			if right.Type == "redirected_statement" && len(right.Children) >= 2 {
				inner := right.Children[0]
				redirs := right.Children[1:]
				listNode := mk(p, "list", left.StartIndex, inner.EndIndex,
					[]*TsNode{left, op, inner})
				lastR := redirs[len(redirs)-1]
				left = mk(p, "redirected_statement", listNode.StartIndex,
					lastR.EndIndex, append([]*TsNode{listNode}, redirs...))
			} else {
				left = mk(p, "list", left.StartIndex, right.EndIndex,
					[]*TsNode{left, op, right})
			}
		} else {
			restoreLex(p.L, save)
			break
		}
	}
	return left
}

// skipNewlines 跳过换行 token
func skipNewlines(p *ParseState) {
	for {
		save := saveLex(p.L)
		t := NextToken(p.L, CtxCmd)
		if t.Type != TokNewline {
			restoreLex(p.L, save)
			break
		}
	}
}

// parsePipeline | 或 |& 连接的命令链（对齐 parsePipeline）。
// tree-sitter 怪癖：`a | b 2>nul | c` 把 b 的重定向提升包住前面的
// pipeline 片段——pipeline(redirected_statement(pipeline(a,|,b),2>nul),|,c)
func parsePipeline(p *ParseState) *TsNode {
	first := parseCommand(p)
	if first == nil {
		return nil
	}
	parts := []*TsNode{first}
	for {
		save := saveLex(p.L)
		t := NextToken(p.L, CtxCmd)
		if t.Type == TokOp && (t.Value == "|" || t.Value == "|&") {
			op := leaf(p, t.Value, t)
			skipNewlines(p)
			next := parseCommand(p)
			if next == nil {
				parts = append(parts, op)
				break
			}
			// next 的尾随重定向提升包住当前片段
			if next.Type == "redirected_statement" &&
				len(next.Children) >= 2 && len(parts) >= 1 {
				inner := next.Children[0]
				redirs := next.Children[1:]
				pipeKids := append(append([]*TsNode{}, parts...), op, inner)
				pipeNode := mk(p, "pipeline", pipeKids[0].StartIndex,
					inner.EndIndex, pipeKids)
				lastR := redirs[len(redirs)-1]
				wrapped := mk(p, "redirected_statement", pipeNode.StartIndex,
					lastR.EndIndex, append([]*TsNode{pipeNode}, redirs...))
				parts = parts[:0]
				parts = append(parts, wrapped)
				continue
			}
			parts = append(parts, op, next)
		} else {
			restoreLex(p.L, save)
			break
		}
	}
	if len(parts) == 1 {
		return parts[0]
	}
	last := parts[len(parts)-1]
	return mk(p, "pipeline", parts[0].StartIndex, last.EndIndex, parts)
}
