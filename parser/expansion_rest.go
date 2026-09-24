package parser

// expansion_rest.go：展开剩余段与反引号（对齐 bashParser.ts 2807-3150 行）：
// parseExpansionRest / parseExpansionRegexSegmented / parseBacktick。

// parseExpansionRest 展开操作符后的剩余段（对齐 parseExpansionRest）。
// 不 skipBlanks——`${var:- }` 的空格就是词本身。停在 } 或换行
// （`${var:\n}` 不产出词）。stopAtSlash=true 时在 `/` 停（${var/pat/repl}
// 的 pat/repl 切分）。nodeType 'replword' 是 `/` `//` 替换段的 word 模式
// ——同 'word' 但 `(` 不解析为数组
func parseExpansionRest(p *ParseState, nodeType string, stopAtSlash bool) *TsNode {
	start := p.L.b
	// 值替换 RHS 以 `(` 开头解析为数组：${var:-(x)} →
	// (expansion (variable_name) (array (word)))。仅 'word' 语境
	//（pattern 操作符产出 regex；'replword' 的 `(` 按 grammar
	// _expansion_regex_replacement 是普通字符）
	if nodeType == "word" && peek(p.L, 0) == '(' {
		advance(p.L)
		open := mk(p, "(", start, p.L.b, nil)
		elems := []*TsNode{open}
		for p.L.i < p.L.len {
			skipBlanks(p.L)
			c := peek(p.L, 0)
			if c == ')' || c == '}' || c == '\n' || c == 0 {
				break
			}
			wStart := p.L.b
			for p.L.i < p.L.len {
				wc := peek(p.L, 0)
				if wc == ')' || wc == '}' || wc == ' ' || wc == '\t' ||
					wc == '\n' || wc == 0 {
					break
				}
				advance(p.L)
			}
			if p.L.b > wStart {
				elems = append(elems, mk(p, "word", wStart, p.L.b, nil))
			} else {
				break
			}
		}
		if peek(p.L, 0) == ')' {
			cStart := p.L.b
			advance(p.L)
			elems = append(elems, mk(p, ")", cStart, p.L.b, nil))
		}
		for peek(p.L, 0) == '\n' {
			advance(p.L)
		}
		return mk(p, "array", start, p.L.b, elems)
	}
	// REGEX 模式：平铺单区间扫描。引号不透明（跳过使其中的 `/` 不破坏
	// stopAtSlash），但不产出独立节点——整个区间成为一个 regex 节点
	if nodeType == "regex" {
		braceDepth := 0
		for p.L.i < p.L.len {
			c := peek(p.L, 0)
			if c == '\n' {
				break
			}
			if braceDepth == 0 {
				if c == '}' {
					break
				}
				if stopAtSlash && c == '/' {
					break
				}
			}
			if c == '\\' && p.L.i+1 < p.L.len {
				advance(p.L)
				advance(p.L)
				continue
			}
			if c == '"' || c == '\'' {
				advance(p.L)
				for p.L.i < p.L.len && peek(p.L, 0) != c {
					if peek(p.L, 0) == '\\' && p.L.i+1 < p.L.len {
						advance(p.L)
					}
					advance(p.L)
				}
				if peek(p.L, 0) == c {
					advance(p.L)
				}
				continue
			}
			// 跳过嵌套 ${...} $(...) $[...]，使其中的 } / 不终止本段
			if c == '$' {
				c1 := peek(p.L, 1)
				if c1 == '{' {
					d := 0
					advance(p.L)
					advance(p.L)
					d++
					for p.L.i < p.L.len && d > 0 {
						nc := peek(p.L, 0)
						if nc == '{' {
							d++
						} else if nc == '}' {
							d--
						}
						advance(p.L)
					}
					continue
				}
				if c1 == '(' {
					d := 0
					advance(p.L)
					advance(p.L)
					d++
					for p.L.i < p.L.len && d > 0 {
						nc := peek(p.L, 0)
						if nc == '(' {
							d++
						} else if nc == ')' {
							d--
						}
						advance(p.L)
					}
					continue
				}
			}
			if c == '{' {
				braceDepth++
			} else if c == '}' && braceDepth > 0 {
				braceDepth--
			}
			advance(p.L)
		}
		end := p.L.b
		for peek(p.L, 0) == '\n' {
			advance(p.L)
		}
		if end == start {
			return nil
		}
		return mk(p, "regex", start, end, nil)
	}
	// WORD 模式：分段解析器——识别嵌套 ${...}、$(...)、$'...'、"..."、
	// '...'、$ident、<(...)/>(...)；裸字符累积为 word 片段。多片段 →
	// 包 concatenation
	var parts []*TsNode
	segStart := p.L.b
	braceDepth := 0
	flushSeg := func() {
		if p.L.b > segStart {
			parts = append(parts, mk(p, "word", segStart, p.L.b, nil))
		}
	}
	for p.L.i < p.L.len {
		c := peek(p.L, 0)
		if c == '\n' {
			break
		}
		if braceDepth == 0 {
			if c == '}' {
				break
			}
			if stopAtSlash && c == '/' {
				break
			}
		}
		if c == '\\' && p.L.i+1 < p.L.len {
			advance(p.L)
			advance(p.L)
			continue
		}
		c1 := peek(p.L, 1)
		if c == '$' {
			if c1 == '{' || c1 == '(' || c1 == '[' {
				flushSeg()
				if exp := parseDollarLike(p); exp != nil {
					parts = append(parts, exp)
				}
				segStart = p.L.b
				continue
			}
			if c1 == '\'' {
				// $'...' ANSI-C 字符串
				flushSeg()
				aStart := p.L.b
				advance(p.L)
				advance(p.L)
				for p.L.i < p.L.len && peek(p.L, 0) != '\'' {
					if peek(p.L, 0) == '\\' && p.L.i+1 < p.L.len {
						advance(p.L)
					}
					advance(p.L)
				}
				if peek(p.L, 0) == '\'' {
					advance(p.L)
				}
				parts = append(parts, mk(p, "ansi_c_string", aStart, p.L.b, nil))
				segStart = p.L.b
				continue
			}
			if isIdentStart(c1) || isDigit(c1) || specialVars[c1] {
				flushSeg()
				if exp := parseDollarLike(p); exp != nil {
					parts = append(parts, exp)
				}
				segStart = p.L.b
				continue
			}
		}
		if c == '"' {
			flushSeg()
			parts = append(parts, parseDoubleQuoted(p))
			segStart = p.L.b
			continue
		}
		if c == '\'' {
			flushSeg()
			rStart := p.L.b
			advance(p.L)
			for p.L.i < p.L.len && peek(p.L, 0) != '\'' {
				advance(p.L)
			}
			if peek(p.L, 0) == '\'' {
				advance(p.L)
			}
			parts = append(parts, mk(p, "raw_string", rStart, p.L.b, nil))
			segStart = p.L.b
			continue
		}
		if (c == '<' || c == '>') && c1 == '(' {
			flushSeg()
			if ps := parseProcessSub(p); ps != nil {
				parts = append(parts, ps)
			}
			segStart = p.L.b
			continue
		}
		if c == '`' {
			flushSeg()
			if bt := parseBacktick(p); bt != nil {
				parts = append(parts, bt)
			}
			segStart = p.L.b
			continue
		}
		// 花括号深度跟踪：嵌套 {a,b} 展开字符不得提前终止
		//（罕见但 `${cond}? (` 的 `?` 应按词处理）
		if c == '{' {
			braceDepth++
		} else if c == '}' && braceDepth > 0 {
			braceDepth--
		}
		advance(p.L)
	}
	flushSeg()
	// 消费 } 前的尾随换行让调用方看到 }
	for peek(p.L, 0) == '\n' {
		advance(p.L)
	}
	// tree-sitter 在展开 RHS 有后续内容时跳过前导空白（extras）：
	// `${2+ ${2}}` → 仅 (expansion)。但 `${v:- }`（纯空格 RHS）保留空格
	// 为 (word)。非唯一片段时丢弃前导纯空白 word 片段
	if len(parts) > 1 && parts[0].Type == "word" && isBlankOnly(parts[0].Text) {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		return parts[0]
	}
	last := parts[len(parts)-1]
	return mk(p, "concatenation", parts[0].StartIndex, last.EndIndex, parts)
}

// parseExpansionRegexSegmented # ## % %% 操作符的 segmented 模式——
// 按 grammar _expansion_regex：repeat(choice(regex, string, raw_string,
// ')', /\s+/→regex))。每个引号串成为兄弟节点不被吸收。
// `${f%'str'*}` → (raw_string)(regex)
func parseExpansionRegexSegmented(p *ParseState) []*TsNode {
	var out []*TsNode
	segStart := p.L.b
	flushRegex := func() {
		if p.L.b > segStart {
			out = append(out, mk(p, "regex", segStart, p.L.b, nil))
		}
	}
	for p.L.i < p.L.len {
		c := peek(p.L, 0)
		if c == '}' || c == '\n' {
			break
		}
		if c == '\\' && p.L.i+1 < p.L.len {
			advance(p.L)
			advance(p.L)
			continue
		}
		if c == '"' {
			flushRegex()
			out = append(out, parseDoubleQuoted(p))
			segStart = p.L.b
			continue
		}
		if c == '\'' {
			flushRegex()
			rStart := p.L.b
			advance(p.L)
			for p.L.i < p.L.len && peek(p.L, 0) != '\'' {
				advance(p.L)
			}
			if peek(p.L, 0) == '\'' {
				advance(p.L)
			}
			out = append(out, mk(p, "raw_string", rStart, p.L.b, nil))
			segStart = p.L.b
			continue
		}
		// 嵌套 ${...} $(...)——不透明扫描使其中的 } 不终止本段
		if c == '$' {
			c1 := peek(p.L, 1)
			if c1 == '{' {
				d := 1
				advance(p.L)
				advance(p.L)
				for p.L.i < p.L.len && d > 0 {
					nc := peek(p.L, 0)
					if nc == '{' {
						d++
					} else if nc == '}' {
						d--
					}
					advance(p.L)
				}
				continue
			}
			if c1 == '(' {
				d := 1
				advance(p.L)
				advance(p.L)
				for p.L.i < p.L.len && d > 0 {
					nc := peek(p.L, 0)
					if nc == '(' {
						d++
					} else if nc == ')' {
						d--
					}
					advance(p.L)
				}
				continue
			}
		}
		advance(p.L)
	}
	flushRegex()
	for peek(p.L, 0) == '\n' {
		advance(p.L)
	}
	return out
}

// parseBacktick 反引号命令替换（对齐 parseBacktick）
func parseBacktick(p *ParseState) *TsNode {
	start := p.L.b
	advance(p.L)
	open := mk(p, "`", start, p.L.b, nil)
	p.inBacktick++
	// 内联解析语句——遇闭合反引号停
	var body []*TsNode
	for {
		skipBlanks(p.L)
		if peek(p.L, 0) == '`' || peek(p.L, 0) == 0 {
			break
		}
		save := saveLex(p.L)
		t := NextToken(p.L, CtxCmd)
		if t.Type == TokEOF || t.Type == TokBacktick {
			restoreLex(p.L, save)
			break
		}
		if t.Type == TokNewline {
			continue
		}
		restoreLex(p.L, save)
		stmt := parseAndOr(p)
		if stmt == nil {
			break
		}
		body = append(body, stmt)
		skipBlanks(p.L)
		if peek(p.L, 0) == '`' {
			break
		}
		save2 := saveLex(p.L)
		sep := NextToken(p.L, CtxCmd)
		if sep.Type == TokOp && (sep.Value == ";" || sep.Value == "&") {
			body = append(body, leaf(p, sep.Value, sep))
		} else if sep.Type != TokNewline {
			restoreLex(p.L, save2)
		}
	}
	p.inBacktick--
	var close *TsNode
	if peek(p.L, 0) == '`' {
		cStart := p.L.b
		advance(p.L)
		close = mk(p, "`", cStart, p.L.b, nil)
	} else {
		close = mk(p, "`", p.L.b, p.L.b, nil)
	}
	// 空反引号（仅空白/换行）被 tree-sitter 完全略除——用作续行技巧：
	// "foo"`<换行>`"bar" → (concatenation (string)(string))，无
	// command_substitution
	if len(body) == 0 {
		return nil
	}
	return mk(p, "command_substitution", start, close.EndIndex,
		append([]*TsNode{open}, append(body, close)...))
}
