package parser

// lexer_token.go：skipBlanks 与 nextToken（对齐 bashParser.ts 272-591 行）。
// nextToken 分支序严格对齐上游（最长操作符优先），勿调整顺序。

// skipBlanks 跳过空白：空格/tab/\r（tree-sitter extras /\s/ 处理 CRLF）；
// 续行 \\\r?\n；\<space>/\<tab> 转义空白（tree-sitter _whitespace /\\?[ \t\v]+/）
func skipBlanks(L *Lexer) {
	for L.i < L.len {
		c := L.runes[L.i]
		if c == ' ' || c == '\t' || c == '\r' {
			advance(L)
		} else if c == '\\' {
			nx := rune(0)
			if L.i+1 < L.len {
				nx = L.runes[L.i+1]
			}
			if nx == '\n' || (nx == '\r' && L.i+2 < L.len && L.runes[L.i+2] == '\n') {
				advance(L)
				advance(L)
				if nx == '\r' {
					advance(L)
				}
			} else if nx == ' ' || nx == '\t' {
				advance(L)
				advance(L)
			} else {
				break
			}
		} else {
			break
		}
	}
}

// LexContext 词法上下文：cmd 态 [ [[ { 为操作符；arg 态为词字符
type LexContext string

const (
	CtxCmd LexContext = "cmd"
	CtxArg LexContext = "arg"
)

// NextToken 扫描下一个 token（上下文敏感，对齐 nextToken）
func NextToken(L *Lexer, ctx LexContext) Token {
	skipBlanks(L)
	start := L.b
	if L.i >= L.len {
		return Token{Type: TokEOF, Value: "", Start: start, End: start}
	}

	c := L.runes[L.i]
	c1 := peek(L, 1)
	c2 := peek(L, 2)

	if c == '\n' {
		advance(L)
		return Token{TokNewline, "\n", start, L.b}
	}

	if c == '#' {
		si := L.i
		for L.i < L.len && L.runes[L.i] != '\n' {
			advance(L)
		}
		return Token{TokComment, sliceRunes(L, si, L.i), start, L.b}
	}

	// 多字符操作符（最长匹配优先）
	if c == '&' && c1 == '&' {
		advance(L)
		advance(L)
		return Token{TokOp, "&&", start, L.b}
	}
	if c == '|' && c1 == '|' {
		advance(L)
		advance(L)
		return Token{TokOp, "||", start, L.b}
	}
	if c == '|' && c1 == '&' {
		advance(L)
		advance(L)
		return Token{TokOp, "|&", start, L.b}
	}
	if c == ';' && c1 == ';' && c2 == '&' {
		advance(L)
		advance(L)
		advance(L)
		return Token{TokOp, ";;&", start, L.b}
	}
	if c == ';' && c1 == ';' {
		advance(L)
		advance(L)
		return Token{TokOp, ";;", start, L.b}
	}
	if c == ';' && c1 == '&' {
		advance(L)
		advance(L)
		return Token{TokOp, ";&", start, L.b}
	}
	if c == '>' && c1 == '>' {
		advance(L)
		advance(L)
		return Token{TokOp, ">>", start, L.b}
	}
	if c == '>' && c1 == '&' && c2 == '-' {
		advance(L)
		advance(L)
		advance(L)
		return Token{TokOp, ">&-", start, L.b}
	}
	if c == '>' && c1 == '&' {
		advance(L)
		advance(L)
		return Token{TokOp, ">&", start, L.b}
	}
	if c == '>' && c1 == '|' {
		advance(L)
		advance(L)
		return Token{TokOp, ">|", start, L.b}
	}
	if c == '&' && c1 == '>' && c2 == '>' {
		advance(L)
		advance(L)
		advance(L)
		return Token{TokOp, "&>>", start, L.b}
	}
	if c == '&' && c1 == '>' {
		advance(L)
		advance(L)
		return Token{TokOp, "&>", start, L.b}
	}
	if c == '<' && c1 == '<' && c2 == '<' {
		advance(L)
		advance(L)
		advance(L)
		return Token{TokOp, "<<<", start, L.b}
	}
	if c == '<' && c1 == '<' && c2 == '-' {
		advance(L)
		advance(L)
		advance(L)
		return Token{TokOp, "<<-", start, L.b}
	}
	if c == '<' && c1 == '<' {
		advance(L)
		advance(L)
		return Token{TokOp, "<<", start, L.b}
	}
	if c == '<' && c1 == '&' && c2 == '-' {
		advance(L)
		advance(L)
		advance(L)
		return Token{TokOp, "<&-", start, L.b}
	}
	if c == '<' && c1 == '&' {
		advance(L)
		advance(L)
		return Token{TokOp, "<&", start, L.b}
	}
	if c == '<' && c1 == '(' {
		advance(L)
		advance(L)
		return Token{TokLtParen, "<(", start, L.b}
	}
	if c == '>' && c1 == '(' {
		advance(L)
		advance(L)
		return Token{TokGtParen, ">(", start, L.b}
	}
	if c == '(' && c1 == '(' {
		advance(L)
		advance(L)
		return Token{TokOp, "((", start, L.b}
	}
	if c == ')' && c1 == ')' {
		advance(L)
		advance(L)
		return Token{TokOp, "))", start, L.b}
	}

	if c == '|' || c == '&' || c == ';' || c == '>' || c == '<' {
		advance(L)
		return Token{TokOp, string(c), start, L.b}
	}
	if c == '(' || c == ')' {
		advance(L)
		return Token{TokOp, string(c), start, L.b}
	}

	// cmd 位置：[ [[ { 开启 test/组；arg 位置它们是词字符
	if ctx == CtxCmd {
		if c == '[' && c1 == '[' {
			advance(L)
			advance(L)
			return Token{TokOp, "[[", start, L.b}
		}
		if c == '[' {
			advance(L)
			return Token{TokOp, "[", start, L.b}
		}
		if c == '{' && (c1 == ' ' || c1 == '\t' || c1 == '\n') {
			advance(L)
			return Token{TokOp, "{", start, L.b}
		}
		if c == '}' {
			advance(L)
			return Token{TokOp, "}", start, L.b}
		}
		if c == '!' && (c1 == ' ' || c1 == '\t') {
			advance(L)
			return Token{TokOp, "!", start, L.b}
		}
	}

	if c == '"' {
		advance(L)
		return Token{TokDquote, `"`, start, L.b}
	}
	if c == '\'' {
		si := L.i
		advance(L)
		for L.i < L.len && L.runes[L.i] != '\'' {
			advance(L)
		}
		if L.i < L.len {
			advance(L)
		}
		return Token{TokSquote, sliceRunes(L, si, L.i), start, L.b}
	}

	if c == '$' {
		if c1 == '(' && c2 == '(' {
			advance(L)
			advance(L)
			advance(L)
			return Token{TokDollarDParen, "$((", start, L.b}
		}
		if c1 == '(' {
			advance(L)
			advance(L)
			return Token{TokDollarParen, "$(", start, L.b}
		}
		if c1 == '{' {
			advance(L)
			advance(L)
			return Token{TokDollarBrace, "${", start, L.b}
		}
		if c1 == '\'' {
			// ANSI-C 字符串 $'...'
			si := L.i
			advance(L)
			advance(L)
			for L.i < L.len && L.runes[L.i] != '\'' {
				if L.runes[L.i] == '\\' && L.i+1 < L.len {
					advance(L)
				}
				advance(L)
			}
			if L.i < L.len {
				advance(L)
			}
			return Token{TokAnsiC, sliceRunes(L, si, L.i), start, L.b}
		}
		advance(L)
		return Token{TokDollar, "$", start, L.b}
	}

	if c == '`' {
		advance(L)
		return Token{TokBacktick, "`", start, L.b}
	}

	// 重定向前的文件描述符：digit+ 紧跟 > 或 <
	if isDigit(c) {
		j := L.i
		for j < L.len && isDigit(L.runes[j]) {
			j++
		}
		after := rune(0)
		if j < L.len {
			after = L.runes[j]
		}
		if after == '>' || after == '<' {
			si := L.i
			for L.i < j {
				advance(L)
			}
			return Token{TokWord, sliceRunes(L, si, L.i), start, L.b}
		}
	}

	// 词 / 数字
	if isWordStart(c) || c == '{' || c == '}' {
		si := L.i
		for L.i < L.len {
			ch := L.runes[L.i]
			if ch == '\\' {
				if L.i+1 >= L.len {
					// EOF 前孤立 \——tree-sitter 不计入词并产出兄弟 ERROR
					break
				}
				// 转义下一字符（含词中续行 \n）
				if L.runes[L.i+1] == '\n' {
					advance(L)
					advance(L)
					continue
				}
				advance(L)
				advance(L)
				continue
			}
			if !isWordChar(ch) && ch != '{' && ch != '}' {
				break
			}
			advance(L)
		}
		if L.i > si {
			v := sliceRunes(L, si, L.i)
			if isAllDigitsSigned(v) {
				return Token{TokNumber, v, start, L.b}
			}
			return Token{TokWord, v, start, L.b}
		}
		// 空词（EOF 前孤立 \）——落到单字符消费
	}

	// 未知字符——单字符词消费
	advance(L)
	return Token{TokWord, string(c), start, L.b}
}
