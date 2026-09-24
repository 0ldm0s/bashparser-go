package parser

// heredoc.go：heredoc 重定向与体扫描（对齐 bashParser.ts 1658-1927 行）。

// parseHeredocRedirect heredoc 重定向（<< 与 <<-，对齐 tryParseRedirect
// 的 heredoc 分支）
func parseHeredocRedirect(p *ParseState, t Token, v string, fd *TsNode) *TsNode {
	op := leaf(p, v, t)
	// heredoc 开始——定界符词（可带引号）
	skipBlanks(p.L)
	dStart := p.L.b
	quoted := false
	delim := ""
	dc := peek(p.L, 0)
	if dc == '\'' || dc == '"' {
		quoted = true
		advance(p.L)
		for p.L.i < p.L.len && peek(p.L, 0) != dc {
			delim += string(peek(p.L, 0))
			advance(p.L)
		}
		if p.L.i < p.L.len {
			advance(p.L)
		}
	} else if dc == '\\' {
		// 反斜杠转义定界符 \X——恰一个转义字符，体按字面（quoted）。
		// 覆盖 <<\EOF <<\' <<\\ 等
		quoted = true
		advance(p.L)
		if p.L.i < p.L.len && peek(p.L, 0) != '\n' {
			delim += string(peek(p.L, 0))
			advance(p.L)
		}
		// 后续可有更多 ident 字符（<<\EOF → delim "EOF"）
		for p.L.i < p.L.len && isIdentChar(peek(p.L, 0)) {
			delim += string(peek(p.L, 0))
			advance(p.L)
		}
	} else {
		// 未加引号定界符：bash 接受大多数非元字符（不止标识符），
		// 停在 shell 元字符
		for p.L.i < p.L.len && isHeredocDelimChar(peek(p.L, 0)) {
			delim += string(peek(p.L, 0))
			advance(p.L)
		}
	}
	dEnd := p.L.b
	startNode := mk(p, "heredoc_start", dStart, dEnd, nil)
	// 登记待扫描 heredoc——体在下一个换行扫描
	p.L.heredocs = append(p.L.heredocs, heredocPending{
		delim:     delim,
		stripTabs: v == "<<-",
		quoted:    quoted,
	})
	kids := []*TsNode{op, startNode}
	startIdx := op.StartIndex
	if fd != nil {
		startIdx = fd.StartIndex
		kids = append([]*TsNode{fd}, kids...)
	}
	// 安全（上游注释要点）：tree-sitter 把 heredoc_start 与换行之间出现的
	// pipeline/list/file_redirect 嵌为 heredoc_redirect 的子节点；
	// `ls <<'EOF' | rm -rf /tmp/evil` 不能静默丢掉 rm。正确解析尾随词与
	// file_redirect（ast.ts walkHeredocRedirect 对未识别子节点 fail-closed）；
	// pipeline/list 操作符结构性复杂——产出 ERROR 走同一条 fail-closed 拒绝
	for {
		skipBlanks(p.L)
		tc := peek(p.L, 0)
		if tc == '\n' || tc == 0 || p.L.i >= p.L.len {
			break
		}
		// 定界符后的 file redirect：cat <<EOF > out.txt
		if tc == '>' || tc == '<' || isDigit(tc) {
			rSave := saveLex(p.L)
			r := tryParseRedirect(p, false)
			if r != nil && r.Type == "file_redirect" {
				kids = append(kids, r)
				continue
			}
			restoreLex(p.L, rSave)
		}
		// heredoc_start 后的 pipeline：`one <<EOF | grep two`——嵌为
		// heredoc_redirect 子节点（ast.ts walkHeredocRedirect fail-closed）
		if tc == '|' && peek(p.L, 1) != '|' {
			advance(p.L)
			skipBlanks(p.L)
			var pipeCmds []*TsNode
			for {
				cmd := parseCommand(p)
				if cmd == nil {
					break
				}
				pipeCmds = append(pipeCmds, cmd)
				skipBlanks(p.L)
				if peek(p.L, 0) == '|' && peek(p.L, 1) != '|' {
					ps := p.L.b
					advance(p.L)
					pipeCmds = append(pipeCmds, mk(p, "|", ps, p.L.b, nil))
					skipBlanks(p.L)
					continue
				}
				break
			}
			if len(pipeCmds) > 0 {
				pl := pipeCmds[len(pipeCmds)-1]
				// tree-sitter 在 `|` 后总是包 pipeline（哪怕单命令）
				kids = append(kids, mk(p, "pipeline",
					pipeCmds[0].StartIndex, pl.EndIndex, pipeCmds))
			}
			continue
		}
		// heredoc_start 后的 && ||：`cat <<-EOF || die "..."`——tree-sitter
		// 只把 RHS 命令（非 list）嵌为 heredoc_redirect 子节点
		if (tc == '&' && peek(p.L, 1) == '&') || (tc == '|' && peek(p.L, 1) == '|') {
			advance(p.L)
			advance(p.L)
			skipBlanks(p.L)
			if rhs := parseCommand(p); rhs != nil {
				kids = append(kids, rhs)
			}
			continue
		}
		// 终止符/未处理元字符——行剩余消费为 ERROR 交 ast.ts 拒绝。
		// 覆盖 ; & ( )
		if tc == '&' || tc == ';' || tc == '(' || tc == ')' {
			eStart := p.L.b
			for p.L.i < p.L.len && peek(p.L, 0) != '\n' {
				advance(p.L)
			}
			kids = append(kids, mk(p, "ERROR", eStart, p.L.b, nil))
			break
		}
		// 尾随词参数：newins <<-EOF - org.freedesktop.service
		if w := parseWord(p, CtxArg); w != nil {
			kids = append(kids, w)
			continue
		}
		// 未识别——行剩余消费为 ERROR
		eStart := p.L.b
		for p.L.i < p.L.len && peek(p.L, 0) != '\n' {
			advance(p.L)
		}
		if p.L.b > eStart {
			kids = append(kids, mk(p, "ERROR", eStart, p.L.b, nil))
		}
		break
	}
	return mk(p, "heredoc_redirect", startIdx, p.L.b, kids)
}

// scanHeredocBodies 扫描待处理 heredoc 的体（对齐 scanHeredocBodies）
func scanHeredocBodies(p *ParseState) {
	// 不在换行处则先跳到换行
	for p.L.i < p.L.len && p.L.runes[p.L.i] != '\n' {
		advance(p.L)
	}
	if p.L.i < p.L.len {
		advance(p.L)
	}
	for i := range p.L.heredocs {
		hd := &p.L.heredocs[i]
		hd.bodyStart = p.L.b
		delimLen := len([]rune(hd.delim))
		for p.L.i < p.L.len {
			lineStart := p.L.i
			lineStartB := p.L.b
			// <<- 跳过前导 tab
			checkI := lineStart
			if hd.stripTabs {
				for checkI < p.L.len && p.L.runes[checkI] == '\t' {
					checkI++
				}
			}
			// 本行是否为定界符行
			if checkI+delimLen <= p.L.len &&
				string(p.L.runes[checkI:checkI+delimLen]) == hd.delim {
				after := rune(0)
				if checkI+delimLen < p.L.len {
					after = p.L.runes[checkI+delimLen]
				}
				if checkI+delimLen >= p.L.len || after == '\n' || after == '\r' {
					hd.bodyEnd = lineStartB
					// 越过 tab
					for p.L.i < checkI {
						advance(p.L)
					}
					hd.endStart = p.L.b
					// 越过定界符
					for k := 0; k < delimLen; k++ {
						advance(p.L)
					}
					hd.endEnd = p.L.b
					// 跳过尾随换行
					if p.L.i < p.L.len && p.L.runes[p.L.i] == '\n' {
						advance(p.L)
					}
					return
				}
			}
			// 消费整行
			for p.L.i < p.L.len && p.L.runes[p.L.i] != '\n' {
				advance(p.L)
			}
			if p.L.i < p.L.len {
				advance(p.L)
			}
		}
		// 未闭合
		hd.bodyEnd = p.L.b
		hd.endStart = p.L.b
		hd.endEnd = p.L.b
	}
}

// parseHeredocBodyContent 解析未加引号 heredoc 体内的展开（对齐
// parseHeredocBodyContent）：
// tree-sitter-bash 的 heredoc_body 规则隐藏首段文本——只有首个展开之后
// 的内容产出为 heredoc_content；无展开时 heredoc_body 为叶子节点
func parseHeredocBodyContent(p *ParseState, start, end int) []*TsNode {
	saved := saveLex(p.L)
	restoreLexToByte(p, start)
	var out []*TsNode
	contentStart := p.L.b
	sawExpansion := false
	for p.L.b < end {
		c := peek(p.L, 0)
		// 反斜杠抑制展开：\$ \` 字面保留
		if c == '\\' {
			nxt := peek(p.L, 1)
			if nxt == '$' || nxt == '`' || nxt == '\\' {
				advance(p.L)
				advance(p.L)
				continue
			}
			advance(p.L)
			continue
		}
		if c == '$' || c == '`' {
			preB := p.L.b
			exp := parseDollarLike(p)
			// 裸 $ 后接非名字字符（如正则里的 $'）返回孤立 '$' 叶子——
			// 按字面内容处理，不切分
			if exp != nil && (exp.Type == "simple_expansion" ||
				exp.Type == "expansion" || exp.Type == "command_substitution" ||
				exp.Type == "arithmetic_expansion") {
				if sawExpansion && preB > contentStart {
					out = append(out, mk(p, "heredoc_content", contentStart, preB, nil))
				}
				out = append(out, exp)
				contentStart = p.L.b
				sawExpansion = true
			}
			continue
		}
		advance(p.L)
	}
	if sawExpansion {
		out = append(out, mk(p, "heredoc_content", contentStart, end, nil))
	}
	restoreLex(p.L, saved)
	return out
}
