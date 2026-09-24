package parser

// command.go：命令层解析（对齐 bashParser.ts 995-1141 行）：
// parseCommand（全部分支）/ parseSimpleCommand。

// parseCommand 单命令：simple、复合或控制结构（对齐 parseCommand）
func parseCommand(p *ParseState) *TsNode {
	skipBlanks(p.L)
	save := saveLex(p.L)
	t := NextToken(p.L, CtxCmd)

	if t.Type == TokEOF {
		restoreLex(p.L, save)
		return nil
	}

	// 取反——tree-sitter 只包命令本身，重定向在外：
	// `! cmd > out` → redirected_statement(negated_command(!,cmd), >out)
	if t.Type == TokOp && t.Value == "!" {
		bang := leaf(p, "!", t)
		inner := parseCommand(p)
		if inner == nil {
			restoreLex(p.L, save)
			return nil
		}
		// inner 为 redirected_statement 时重定向提升到取反之外
		if inner.Type == "redirected_statement" && len(inner.Children) >= 2 {
			cmd := inner.Children[0]
			redirs := inner.Children[1:]
			neg := mk(p, "negated_command", bang.StartIndex, cmd.EndIndex,
				[]*TsNode{bang, cmd})
			lastR := redirs[len(redirs)-1]
			return mk(p, "redirected_statement", neg.StartIndex, lastR.EndIndex,
				append([]*TsNode{neg}, redirs...))
		}
		return mk(p, "negated_command", bang.StartIndex, inner.EndIndex,
			[]*TsNode{bang, inner})
	}

	if t.Type == TokOp && t.Value == "(" {
		open := leaf(p, "(", t)
		body := parseStatements(p, ")")
		closeTok := NextToken(p.L, CtxCmd)
		var close *TsNode
		if closeTok.Type == TokOp && closeTok.Value == ")" {
			close = leaf(p, ")", closeTok)
		} else {
			close = mk(p, ")", open.EndIndex, open.EndIndex, nil)
		}
		node := mk(p, "subshell", open.StartIndex, close.EndIndex,
			append(append([]*TsNode{open}, body...), close))
		return maybeRedirect(p, node, false)
	}

	if t.Type == TokOp && t.Value == "((" {
		open := leaf(p, "((", t)
		exprs := parseArithCommaList(p, "))", "var")
		closeTok := NextToken(p.L, CtxCmd)
		var close *TsNode
		if closeTok.Value == "))" {
			close = leaf(p, "))", closeTok)
		} else {
			close = mk(p, "))", open.EndIndex, open.EndIndex, nil)
		}
		return mk(p, "compound_statement", open.StartIndex, close.EndIndex,
			append(append([]*TsNode{open}, exprs...), close))
	}

	if t.Type == TokOp && t.Value == "{" {
		open := leaf(p, "{", t)
		body := parseStatements(p, "}")
		closeTok := NextToken(p.L, CtxCmd)
		var close *TsNode
		if closeTok.Type == TokOp && closeTok.Value == "}" {
			close = leaf(p, "}", closeTok)
		} else {
			close = mk(p, "}", open.EndIndex, open.EndIndex, nil)
		}
		node := mk(p, "compound_statement", open.StartIndex, close.EndIndex,
			append(append([]*TsNode{open}, body...), close))
		return maybeRedirect(p, node, false)
	}

	if t.Type == TokOp && (t.Value == "[" || t.Value == "[[") {
		return parseTestCommand(p, t)
	}

	if t.Type == TokWord {
		switch t.Value {
		case "if":
			return maybeRedirect(p, parseIf(p, t), true)
		case "while", "until":
			return maybeRedirect(p, parseWhile(p, t), true)
		case "for", "select":
			return maybeRedirect(p, parseFor(p, t), true)
		case "case":
			return maybeRedirect(p, parseCase(p, t), true)
		case "function":
			return parseFunction(p, t)
		}
		if declKeywords[t.Value] {
			return maybeRedirect(p, parseDeclaration(p, t), false)
		}
		if t.Value == "unset" || t.Value == "unsetenv" {
			return maybeRedirect(p, parseUnset(p, t), false)
		}
	}

	restoreLex(p.L, save)
	return parseSimpleCommand(p)
}

// parseSimpleCommand 简单命令：[赋值]* 词 [参数|重定向]*
// 仅一个赋值且无命令时返回 variable_assignment（对齐 1141-1404 行）
func parseSimpleCommand(p *ParseState) *TsNode {
	start := p.L.b
	var assignments []*TsNode
	var preRedirects []*TsNode

	for {
		skipBlanks(p.L)
		a := tryParseAssignment(p)
		if a != nil {
			assignments = append(assignments, a)
			continue
		}
		r := tryParseRedirect(p, false)
		if r != nil {
			preRedirects = append(preRedirects, r)
			continue
		}
		break
	}

	skipBlanks(p.L)
	save := saveLex(p.L)
	nameTok := NextToken(p.L, CtxCmd)
	if nameTok.Type == TokEOF || nameTok.Type == TokNewline ||
		nameTok.Type == TokComment ||
		(nameTok.Type == TokOp &&
			nameTok.Value != "{" && nameTok.Value != "[" && nameTok.Value != "[[") ||
		(nameTok.Type == TokWord &&
			shellKeywords[nameTok.Value] && nameTok.Value != "in") {
		restoreLex(p.L, save)
		// 无命令——独立赋值或重定向
		if len(assignments) == 1 && len(preRedirects) == 0 {
			return assignments[0]
		}
		if len(preRedirects) > 0 && len(assignments) == 0 {
			// 纯重定向 → redirected_statement（仅 file_redirect children）
			last := preRedirects[len(preRedirects)-1]
			return mk(p, "redirected_statement", preRedirects[0].StartIndex,
				last.EndIndex, preRedirects)
		}
		if len(assignments) > 1 && len(preRedirects) == 0 {
			// `A=1 B=2` 无命令 → variable_assignments（复数）
			last := assignments[len(assignments)-1]
			return mk(p, "variable_assignments", assignments[0].StartIndex,
				last.EndIndex, assignments)
		}
		if len(assignments) > 0 || len(preRedirects) > 0 {
			all := append(append([]*TsNode{}, assignments...), preRedirects...)
			last := all[len(all)-1]
			return mk(p, "command", start, last.EndIndex, all)
		}
		return nil
	}
	restoreLex(p.L, save)

	// 函数定义 name() { ... }
	fnSave := saveLex(p.L)
	nm := parseWord(p, CtxCmd)
	if nm != nil && nm.Type == "word" {
		skipBlanks(p.L)
		if peek(p.L, 0) == '(' && peek(p.L, 1) == ')' {
			oTok := NextToken(p.L, CtxCmd)
			cTok := NextToken(p.L, CtxCmd)
			oParen := leaf(p, "(", oTok)
			cParen := leaf(p, ")", cTok)
			skipBlanks(p.L)
			skipNewlines(p)
			body := parseCommand(p)
			if body != nil {
				// body 为 redirected_statement(compound_statement,...) 时
				// 重定向提升到 function_definition 层（grammar 对齐）
				bodyKids := []*TsNode{body}
				if body.Type == "redirected_statement" &&
					len(body.Children) >= 2 &&
					body.Children[0].Type == "compound_statement" {
					bodyKids = body.Children
				}
				last := bodyKids[len(bodyKids)-1]
				return mk(p, "function_definition", nm.StartIndex,
					last.EndIndex, append([]*TsNode{nm, oParen, cParen}, bodyKids...))
			}
		}
	}
	restoreLex(p.L, fnSave)

	nameArg := parseWord(p, CtxCmd)
	if nameArg == nil {
		if len(assignments) == 1 {
			return assignments[0]
		}
		return nil
	}

	cmdName := mk(p, "command_name", nameArg.StartIndex, nameArg.EndIndex,
		[]*TsNode{nameArg})

	var args []*TsNode
	var redirects []*TsNode
	var heredocRedirect *TsNode

	for {
		skipBlanks(p.L)
		// 命令后重定向贪心（repeat1 $._literal，grammar prec.left）：
		// `grep 2>/dev/null -q foo` → file_redirect 吞掉 `-q foo`；
		// 首个重定向前的参数仍归 command（cat a b > out）
		r := tryParseRedirect(p, true)
		if r != nil {
			if r.Type == "heredoc_redirect" {
				heredocRedirect = r
			} else if r.Type == "herestring_redirect" {
				args = append(args, r)
			} else {
				redirects = append(redirects, r)
			}
			continue
		}
		// 出现 file_redirect 后参数结束——grammar 的 command 规则在其
		// post-name choice 中不允许 file_redirect，其后内容归
		// redirected_statement 的 file_redirect children
		if len(redirects) > 0 {
			break
		}
		// `[` test_command 回溯——停在 `]` 让外层消费
		if p.stopToken == "]" && peek(p.L, 0) == ']' {
			break
		}
		save2 := saveLex(p.L)
		pk := NextToken(p.L, CtxArg)
		if pk.Type == TokEOF || pk.Type == TokNewline || pk.Type == TokComment ||
			(pk.Type == TokOp && isArgBreakOp(pk.Value)) {
			restoreLex(p.L, save2)
			break
		}
		restoreLex(p.L, save2)
		arg := parseWord(p, CtxArg)
		if arg == nil {
			// 参数位孤立 `(`——tree-sitter 解析为 subshell 参数
			// （`echo =(cmd)` → command 含 ERROR(=)、subshell(cmd)）
			if peek(p.L, 0) == '(' {
				oTok := NextToken(p.L, CtxCmd)
				open := leaf(p, "(", oTok)
				body := parseStatements(p, ")")
				cTok := NextToken(p.L, CtxCmd)
				var close *TsNode
				if cTok.Type == TokOp && cTok.Value == ")" {
					close = leaf(p, ")", cTok)
				} else {
					close = mk(p, ")", open.EndIndex, open.EndIndex, nil)
				}
				args = append(args, mk(p, "subshell", open.StartIndex,
					close.EndIndex, append(append([]*TsNode{open}, body...), close)))
				continue
			}
			break
		}
		// 参数位孤立 `=` 在 bash 是解析错误——tree-sitter 包 ERROR 恢复
		//（见 `echo =(cmd)`，zsh 进程替换）
		if arg.Type == "word" && arg.Text == "=" {
			args = append(args, mk(p, "ERROR", arg.StartIndex, arg.EndIndex,
				[]*TsNode{arg}))
			continue
		}
		// 词后紧跟 `(`（无空白）是解析错误——bash 不允许 glob 接 subshell
		// 相邻。tree-sitter 把词包 ERROR。捕获 zsh glob 限定符 `*.(e:'cmd':)`
		if (arg.Type == "word" || arg.Type == "concatenation") &&
			peek(p.L, 0) == '(' && p.L.b == arg.EndIndex {
			args = append(args, mk(p, "ERROR", arg.StartIndex, arg.EndIndex,
				[]*TsNode{arg}))
			continue
		}
		args = append(args, arg)
	}

	// preRedirects（如 `2>&1 cat`、`<<<str cmd`）按 grammar 进 command
	// 节点内、command_name 之前——不进 redirected_statement
	cmdChildren := append(append(append([]*TsNode{}, assignments...), preRedirects...), cmdName)
	cmdChildren = append(cmdChildren, args...)
	cmdEnd := cmdName.EndIndex
	if n := len(cmdChildren); n > 0 {
		cmdEnd = cmdChildren[n-1].EndIndex
	}
	cmd := mk(p, "command", cmdChildren[0].StartIndex, cmdEnd, cmdChildren)

	if heredocRedirect != nil {
		// 现在扫描 heredoc 体
		scanHeredocBodies(p)
		if len(p.L.heredocs) > 0 && len(heredocRedirect.Children) >= 2 {
			hd := p.L.heredocs[0]
			p.L.heredocs = p.L.heredocs[1:]
			var bodyKids []*TsNode
			if !hd.quoted {
				bodyKids = parseHeredocBodyContent(p, hd.bodyStart, hd.bodyEnd)
			}
			bodyNode := mk(p, "heredoc_body", hd.bodyStart, hd.bodyEnd, bodyKids)
			endNode := mk(p, "heredoc_end", hd.endStart, hd.endEnd, nil)
			heredocRedirect.Children = append(heredocRedirect.Children,
				bodyNode, endNode)
			heredocRedirect.EndIndex = hd.endEnd
			heredocRedirect.Text = sliceBytes(p, heredocRedirect.StartIndex, hd.endEnd)
		}
		allR := append([]*TsNode{heredocRedirect}, redirects...)
		rStart := cmd.StartIndex
		if len(preRedirects) > 0 && preRedirects[0].StartIndex < rStart {
			rStart = preRedirects[0].StartIndex
		}
		return mk(p, "redirected_statement", rStart, heredocRedirect.EndIndex,
			append([]*TsNode{cmd}, allR...))
	}

	if len(redirects) > 0 {
		last := redirects[len(redirects)-1]
		return mk(p, "redirected_statement", cmd.StartIndex, last.EndIndex,
			append([]*TsNode{cmd}, redirects...))
	}

	return cmd
}

// isArgBreakOp 参数流断点操作符
func isArgBreakOp(v string) bool {
	switch v {
	case "|", "|&", "&&", "||", ";", ";;", ";&", ";;&", "&", ")", "}", "))":
		return true
	}
	return false
}
