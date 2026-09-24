package parser

// expansion.go：${...} 展开体解析（对齐 bashParser.ts 2555-2805 行）。

// parseExpansionBody 解析 ${ 内部（对齐 parseExpansionBody）
func parseExpansionBody(p *ParseState) []*TsNode {
	out := []*TsNode{}
	skipBlanks(p.L)
	// 奇异形态：${#!} ${!#} ${!##} ${!# } ${!## } 全部产出空 (expansion)——
	// # 与 ! 仅互相组合时都成为匿名节点（${!##/} 不匹配，按正常解析）
	{
		c0 := peek(p.L, 0)
		c1 := peek(p.L, 1)
		if c0 == '#' && c1 == '!' && peek(p.L, 2) == '}' {
			advance(p.L)
			advance(p.L)
			return out
		}
		if c0 == '!' && c1 == '#' {
			// ${!#} ${!##} 可选尾随空格后 }
			j := 2
			if peek(p.L, j) == '#' {
				j++
			}
			if peek(p.L, j) == ' ' {
				j++
			}
			if peek(p.L, j) == '}' {
				for ; j > 0; j-- {
					advance(p.L)
				}
				return out
			}
		}
	}
	// 可选 # 前缀（取长度）
	if peek(p.L, 0) == '#' {
		s := p.L.b
		advance(p.L)
		out = append(out, mk(p, "#", s, p.L.b, nil))
	}
	// 可选 ! 前缀（间接展开 ${!varname} ${!prefix*} ${!prefix@}）——
	// 仅当后随标识符；${!} 单独是特殊变量 $!。另 = ~ 前缀（zsh 风格
	// ${=var} ${~var}）
	pc := peek(p.L, 0)
	if (pc == '!' || pc == '=' || pc == '~') &&
		(isIdentStart(peek(p.L, 1)) || isDigit(peek(p.L, 1))) {
		s := p.L.b
		advance(p.L)
		out = append(out, mk(p, string(pc), s, p.L.b, nil))
	}
	skipBlanks(p.L)
	// 变量名
	if isIdentStart(peek(p.L, 0)) {
		s := p.L.b
		for isIdentChar(peek(p.L, 0)) {
			advance(p.L)
		}
		out = append(out, mk(p, "variable_name", s, p.L.b, nil))
	} else if isDigit(peek(p.L, 0)) {
		s := p.L.b
		for isDigit(peek(p.L, 0)) {
			advance(p.L)
		}
		out = append(out, mk(p, "variable_name", s, p.L.b, nil))
	} else if specialVars[peek(p.L, 0)] {
		s := p.L.b
		advance(p.L)
		out = append(out, mk(p, "special_variable_name", s, p.L.b, nil))
	}
	// 可选下标 [idx]——按算术解析
	if peek(p.L, 0) == '[' {
		varNode := out[len(out)-1]
		brOpen := p.L.b
		advance(p.L)
		brOpenNode := mk(p, "[", brOpen, p.L.b, nil)
		idx := parseSubscriptIndexInline(p)
		skipBlanks(p.L)
		brClose := p.L.b
		if peek(p.L, 0) == ']' {
			advance(p.L)
		}
		brCloseNode := mk(p, "]", brClose, p.L.b, nil)
		if varNode != nil {
			kids := []*TsNode{varNode, brOpenNode, brCloseNode}
			if idx != nil {
				kids = []*TsNode{varNode, brOpenNode, idx, brCloseNode}
			}
			out[len(out)-1] = mk(p, "subscript", varNode.StartIndex, p.L.b, kids)
		}
	}
	skipBlanks(p.L)
	// 尾随 * @（间接展开 ${!prefix*} ${!prefix@}）或 @operator
	//（参数变换 ${var@U} ${var@Q}）——匿名
	tc := peek(p.L, 0)
	if (tc == '*' || tc == '@') && peek(p.L, 1) == '}' {
		s := p.L.b
		advance(p.L)
		out = append(out, mk(p, string(tc), s, p.L.b, nil))
		return out
	}
	if tc == '@' && isIdentStart(peek(p.L, 1)) {
		// ${var@U} 变换——@ 匿名，消费操作符字符
		s := p.L.b
		advance(p.L)
		out = append(out, mk(p, "@", s, p.L.b, nil))
		for isIdentChar(peek(p.L, 0)) {
			advance(p.L)
		}
		return out
	}
	// 操作符 :- := :? :+ - = ? + # ## % %% / // ^ ^^ , ,, 等
	c := peek(p.L, 0)
	// 裸 `:` 子串操作符 ${var:off:len}——偏移与长度按算术解析。必须在
	// 通用操作符处理之前，使 `:` 后的 `(` 走括号表达式而非数组路径；
	// `:-` `:=` `:?` `:+`（无空格）仍是默认值操作符；`: -1`（- 前有
	// 空格）是负偏移子串
	if c == ':' {
		c1 := peek(p.L, 1)
		// `:\n` 或 `:}`——空子串展开，不产出（仅 variable_name）
		if c1 == '\n' || c1 == '}' {
			advance(p.L)
			for peek(p.L, 0) == '\n' {
				advance(p.L)
			}
			return out
		}
		if c1 != '-' && c1 != '=' && c1 != '?' && c1 != '+' {
			advance(p.L)
			skipBlanks(p.L)
			// 偏移——算术。顶层 `-N` 按 tree-sitter 是单 number 节点；
			// 括号内是 unary_expression(number)
			offC := peek(p.L, 0)
			var off *TsNode
			if offC == '-' && isDigit(peek(p.L, 1)) {
				ns := p.L.b
				advance(p.L)
				for isDigit(peek(p.L, 0)) {
					advance(p.L)
				}
				off = mk(p, "number", ns, p.L.b, nil)
			} else {
				off = parseArithExpr(p, ":}", "var")
			}
			if off != nil {
				out = append(out, off)
			}
			skipBlanks(p.L)
			if peek(p.L, 0) == ':' {
				advance(p.L)
				skipBlanks(p.L)
				lenC := peek(p.L, 0)
				var length *TsNode
				if lenC == '-' && isDigit(peek(p.L, 1)) {
					ns := p.L.b
					advance(p.L)
					for isDigit(peek(p.L, 0)) {
						advance(p.L)
					}
					length = mk(p, "number", ns, p.L.b, nil)
				} else {
					length = parseArithExpr(p, "}", "var")
				}
				if length != nil {
					out = append(out, length)
				}
			}
			return out
		}
	}
	if c == ':' || c == '#' || c == '%' || c == '/' || c == '^' ||
		c == ',' || c == '-' || c == '=' || c == '?' || c == '+' {
		s := p.L.b
		c1 := peek(p.L, 1)
		op := string(c)
		if c == ':' && (c1 == '-' || c1 == '=' || c1 == '?' || c1 == '+') {
			advance(p.L)
			advance(p.L)
			op = string(c) + string(c1)
		} else if (c == '#' || c == '%' || c == '/' || c == '^' || c == ',') && c1 == c {
			// 双写操作符：## %% // ^^ ,,
			advance(p.L)
			advance(p.L)
			op = string(c) + string(c)
		} else {
			advance(p.L)
		}
		out = append(out, mk(p, op, s, p.L.b, nil))
		// 剩余是默认值/替换——pattern 操作符产出 regex，值替换产出 word。
		// `/` `//` 在下一个 `/` 切分为 (regex)+(word)
		isPattern := op == "#" || op == "##" || op == "%" || op == "%%" ||
			op == "/" || op == "//" || op == "^" || op == "^^" ||
			op == "," || op == ",,"
		if op == "/" || op == "//" {
			// 可选 /# 或 /% 锚定前缀——匿名节点
			ac := peek(p.L, 0)
			if ac == '#' || ac == '%' {
				aStart := p.L.b
				advance(p.L)
				out = append(out, mk(p, string(ac), aStart, p.L.b, nil))
			}
			// 模式：按 grammar _expansion_regex_replacement，pattern 为
			// choice(regex, string, cmd_sub, seq(string, regex))。以 " 开头
			// 则产出 (string)，尾随字符成为 (regex)。`${v//"${old}"/}` →
			// (string(expansion))；`${v//"${c}"\//}` → (string)(regex)
			if peek(p.L, 0) == '"' {
				out = append(out, parseDoubleQuoted(p))
				if tail := parseExpansionRest(p, "regex", true); tail != nil {
					out = append(out, tail)
				}
			} else if regex := parseExpansionRest(p, "regex", true); regex != nil {
				out = append(out, regex)
			}
			if peek(p.L, 0) == '/' {
				sepStart := p.L.b
				advance(p.L)
				out = append(out, mk(p, "/", sepStart, p.L.b, nil))
				// 替换段：grammar 的 choice 含 seq(cmd_sub, word)——产出两个
				// 兄弟（非 concatenation）。替换开头的 `(` 是普通词字符
				// 不是数组——与 `:-` 默认值语境不同。`${v/(/(Gentoo ${x}, }`
				// 的替换 `(Gentoo ${x}, ` 是 (concatenation (word)(expansion)(word))
				repl := parseExpansionRest(p, "replword", false)
				if repl != nil {
					// seq(cmd_sub, word) 特例 → 兄弟节点。检出条件：替换是
					// 恰 2 部分的 concatenation 且首部是 command_substitution
					if repl.Type == "concatenation" &&
						len(repl.Children) == 2 &&
						repl.Children[0].Type == "command_substitution" {
						out = append(out, repl.Children[0])
						out = append(out, repl.Children[1])
					} else {
						out = append(out, repl)
					}
				}
			}
		} else if op == "#" || op == "##" || op == "%" || op == "%%" {
			// 模式删除：grammar _expansion_regex 的 pattern 为
			// repeat(choice(regex, string, raw_string, ')'))——每个引号串
			// 是兄弟节点不被吸收。`${f%'str'*}` → (raw_string)(regex)；
			// `${f/'str'*}`（slash）保持单 regex
			out = append(out, parseExpansionRegexSegmented(p)...)
		} else {
			rest := parseExpansionRest(p, map[bool]string{true: "regex", false: "word"}[isPattern], false)
			if rest != nil {
				out = append(out, rest)
			}
		}
	}
	return out
}
