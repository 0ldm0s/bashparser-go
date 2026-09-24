package parser

// arith.go：算术表达式解析（对齐 bashParser.ts 4071-4437 行）：
// 优先级爬升二元解析 + 三元/逗号列表/一元/后缀/主元。

// arithMode 算术语境模式：
//   - 'var'：裸标识符 → variable_name（默认，$((..))、((..))）
//   - 'word'：裸标识符 → word（c 风格 for 头条件/更新子句）
//   - 'assign'：带 = 的标识符 → variable_assignment（c 风格 for init 子句）
type arithMode string

const (
	arithModeVar    arithMode = "var"
	arithModeWord   arithMode = "word"
	arithModeAssign arithMode = "assign"
)

// arithPrec 操作符优先级表（越大结合越紧，对齐 ARITH_PREC）
var arithPrec = map[string]int{
	"=": 2, "+=": 2, "-=": 2, "*=": 2, "/=": 2, "%=": 2,
	"<<=": 2, ">>=": 2, "&=": 2, "^=": 2, "|=": 2,
	"||": 4, "&&": 5, "|": 6, "^": 7, "&": 8,
	"==": 9, "!=": 9,
	"<": 10, ">": 10, "<=": 10, ">=": 10,
	"<<": 11, ">>": 11,
	"+": 12, "-": 12,
	"*": 13, "/": 13, "%": 13,
	"**": 14,
}

// arithRightAssoc 右结合操作符（赋值与幂）
var arithRightAssoc = map[string]bool{
	"=": true, "+=": true, "-=": true, "*=": true, "/=": true, "%=": true,
	"<<=": true, ">>=": true, "&=": true, "^=": true, "|=": true, "**": true,
}

// parseArithExpr 算术表达式入口（对齐 parseArithExpr）
func parseArithExpr(p *ParseState, stop string, mode arithMode) *TsNode {
	return parseArithTernary(p, stop, mode)
}

// parseArithCommaList 顶层逗号分隔列表（对齐 parseArithCommaList）：
// arithmetic_expansion 产出多个 children
func parseArithCommaList(p *ParseState, stop string, mode arithMode) []*TsNode {
	var out []*TsNode
	for {
		if e := parseArithTernary(p, stop, mode); e != nil {
			out = append(out, e)
		}
		skipBlanks(p.L)
		if peek(p.L, 0) == ',' && !isArithStop(p, stop) {
			advance(p.L)
			continue
		}
		break
	}
	return out
}

// parseArithTernary 三元表达式层（对齐 parseArithTernary）
func parseArithTernary(p *ParseState, stop string, mode arithMode) *TsNode {
	cond := parseArithBinary(p, stop, 0, mode)
	if cond == nil {
		return nil
	}
	skipBlanks(p.L)
	if peek(p.L, 0) == '?' {
		qs := p.L.b
		advance(p.L)
		q := mk(p, "?", qs, p.L.b, nil)
		t := parseArithBinary(p, ":", 0, mode)
		skipBlanks(p.L)
		var colon *TsNode
		if peek(p.L, 0) == ':' {
			cs := p.L.b
			advance(p.L)
			colon = mk(p, ":", cs, p.L.b, nil)
		} else {
			colon = mk(p, ":", p.L.b, p.L.b, nil)
		}
		f := parseArithTernary(p, stop, mode)
		last := colon
		if f != nil {
			last = f
		}
		kids := []*TsNode{cond, q}
		if t != nil {
			kids = append(kids, t)
		}
		kids = append(kids, colon)
		if f != nil {
			kids = append(kids, f)
		}
		return mk(p, "ternary_expression", cond.StartIndex, last.EndIndex, kids)
	}
	return cond
}

// scanArithOp 扫描下一个算术二元操作符；返回 [文本, 长度] 或 nil
// （对齐 scanArithOp）
func scanArithOp(p *ParseState) (string, int, bool) {
	c := peek(p.L, 0)
	c1 := peek(p.L, 1)
	c2 := peek(p.L, 2)
	// 3 字符：<<= >>=
	if c == '<' && c1 == '<' && c2 == '=' {
		return "<<=", 3, true
	}
	if c == '>' && c1 == '>' && c2 == '=' {
		return ">>=", 3, true
	}
	// 2 字符
	two := func(cc, c1c rune, s string) bool { return c == cc && c1 == c1c }
	if two('*', '*', "**") {
		return "**", 2, true
	}
	if two('<', '<', "<<") {
		return "<<", 2, true
	}
	if two('>', '>', ">>") {
		return ">>", 2, true
	}
	if two('=', '=', "==") {
		return "==", 2, true
	}
	if two('!', '=', "!=") {
		return "!=", 2, true
	}
	if two('<', '=', "<=") {
		return "<=", 2, true
	}
	if two('>', '=', ">=") {
		return ">=", 2, true
	}
	if two('&', '&', "&&") {
		return "&&", 2, true
	}
	if two('|', '|', "||") {
		return "||", 2, true
	}
	if two('+', '=', "+=") {
		return "+=", 2, true
	}
	if two('-', '=', "-=") {
		return "-=", 2, true
	}
	if two('*', '=', "*=") {
		return "*=", 2, true
	}
	if two('/', '=', "/=") {
		return "/=", 2, true
	}
	if two('%', '=', "%=") {
		return "%=", 2, true
	}
	if two('&', '=', "&=") {
		return "&=", 2, true
	}
	if two('^', '=', "^=") {
		return "^=", 2, true
	}
	if two('|', '=', "|=") {
		return "|=", 2, true
	}
	// 1 字符——不含 ++ --（那些是前/后缀）
	if c == '+' && c1 != '+' {
		return "+", 1, true
	}
	if c == '-' && c1 != '-' {
		return "-", 1, true
	}
	switch c {
	case '*':
		return "*", 1, true
	case '/':
		return "/", 1, true
	case '%':
		return "%", 1, true
	case '<':
		return "<", 1, true
	case '>':
		return ">", 1, true
	case '&':
		return "&", 1, true
	case '|':
		return "|", 1, true
	case '^':
		return "^", 1, true
	case '=':
		return "=", 1, true
	}
	return "", 0, false
}

// parseArithBinary 优先级爬升二元表达式（对齐 parseArithBinary）
func parseArithBinary(p *ParseState, stop string, minPrec int, mode arithMode) *TsNode {
	left := parseArithUnary(p, stop, mode)
	if left == nil {
		return nil
	}
	for {
		skipBlanks(p.L)
		if isArithStop(p, stop) {
			break
		}
		if peek(p.L, 0) == ',' {
			break
		}
		opText, opLen, ok := scanArithOp(p)
		if !ok {
			break
		}
		prec, has := arithPrec[opText]
		if !has || prec < minPrec {
			break
		}
		os := p.L.b
		for k := 0; k < opLen; k++ {
			advance(p.L)
		}
		op := mk(p, opText, os, p.L.b, nil)
		nextMin := prec + 1
		if arithRightAssoc[opText] {
			nextMin = prec
		}
		right := parseArithBinary(p, stop, nextMin, mode)
		if right == nil {
			break
		}
		left = mk(p, "binary_expression", left.StartIndex, right.EndIndex,
			[]*TsNode{left, op, right})
	}
	return left
}

// parseArithUnary 一元表达式层（对齐 parseArithUnary）
func parseArithUnary(p *ParseState, stop string, mode arithMode) *TsNode {
	skipBlanks(p.L)
	if isArithStop(p, stop) {
		return nil
	}
	c := peek(p.L, 0)
	c1 := peek(p.L, 1)
	// 前缀 ++ --
	if (c == '+' && c1 == '+') || (c == '-' && c1 == '-') {
		s := p.L.b
		advance(p.L)
		advance(p.L)
		op := mk(p, string(c)+string(c1), s, p.L.b, nil)
		inner := parseArithUnary(p, stop, mode)
		if inner == nil {
			return op
		}
		return mk(p, "unary_expression", op.StartIndex, inner.EndIndex,
			[]*TsNode{op, inner})
	}
	if c == '-' || c == '+' || c == '!' || c == '~' {
		// 'word'/'assign' 模式（c 风格 for 头）中 `-N` 按 tree-sitter 是
		// 单 number 字面量而非 unary_expression；'var' 模式用 unary
		if mode != arithModeVar && c == '-' && isDigit(c1) {
			s := p.L.b
			advance(p.L)
			for isDigit(peek(p.L, 0)) {
				advance(p.L)
			}
			return mk(p, "number", s, p.L.b, nil)
		}
		s := p.L.b
		advance(p.L)
		op := mk(p, string(c), s, p.L.b, nil)
		inner := parseArithUnary(p, stop, mode)
		if inner == nil {
			return op
		}
		return mk(p, "unary_expression", op.StartIndex, inner.EndIndex,
			[]*TsNode{op, inner})
	}
	return parseArithPostfix(p, stop, mode)
}

// parseArithPostfix 后缀表达式层（对齐 parseArithPostfix）
func parseArithPostfix(p *ParseState, stop string, mode arithMode) *TsNode {
	prim := parseArithPrimary(p, stop, mode)
	if prim == nil {
		return nil
	}
	c := peek(p.L, 0)
	c1 := peek(p.L, 1)
	if (c == '+' && c1 == '+') || (c == '-' && c1 == '-') {
		s := p.L.b
		advance(p.L)
		advance(p.L)
		op := mk(p, string(c)+string(c1), s, p.L.b, nil)
		return mk(p, "postfix_expression", prim.StartIndex, op.EndIndex,
			[]*TsNode{prim, op})
	}
	return prim
}

// parseArithPrimary 算术主元（对齐 parseArithPrimary）：
// 括号/双引号/$展开/数字（含 0x 十六进制与 BASE#DIGITS）/标识符
// （assign 模式的赋值与下标）
func parseArithPrimary(p *ParseState, stop string, mode arithMode) *TsNode {
	skipBlanks(p.L)
	if isArithStop(p, stop) {
		return nil
	}
	c := peek(p.L, 0)
	if c == '(' {
		s := p.L.b
		advance(p.L)
		open := mk(p, "(", s, p.L.b, nil)
		// 括号表达式可含逗号分隔表达式
		inners := parseArithCommaList(p, ")", mode)
		skipBlanks(p.L)
		var close *TsNode
		if peek(p.L, 0) == ')' {
			cs := p.L.b
			advance(p.L)
			close = mk(p, ")", cs, p.L.b, nil)
		} else {
			close = mk(p, ")", p.L.b, p.L.b, nil)
		}
		return mk(p, "parenthesized_expression", open.StartIndex, close.EndIndex,
			append(append([]*TsNode{open}, inners...), close))
	}
	if c == '"' {
		return parseDoubleQuoted(p)
	}
	if c == '$' {
		return parseDollarLike(p)
	}
	if isDigit(c) {
		s := p.L.b
		for isDigit(peek(p.L, 0)) {
			advance(p.L)
		}
		// 十六进制：0x1f
		if p.L.b-s == 1 && c == '0' && (peek(p.L, 0) == 'x' || peek(p.L, 0) == 'X') {
			advance(p.L)
			for isHexDigit(peek(p.L, 0)) {
				advance(p.L)
			}
		} else if peek(p.L, 0) == '#' {
			// 进制记法：BASE#DIGITS 如 2#1010、16#ff
			advance(p.L)
			for isBaseDigit(peek(p.L, 0)) {
				advance(p.L)
			}
		}
		return mk(p, "number", s, p.L.b, nil)
	}
	if isIdentStart(c) {
		s := p.L.b
		for isIdentChar(peek(p.L, 0)) {
			advance(p.L)
		}
		nc := peek(p.L, 0)
		// 'assign' 模式（c 风格 for init）的赋值：产出 variable_assignment
		// 使链式 `a = b = c = 1` 正确嵌套。其他模式经优先级表把 `=` 当
		// binary_expression 操作符
		if mode == arithModeAssign {
			skipBlanks(p.L)
			ac := peek(p.L, 0)
			ac1 := peek(p.L, 1)
			if ac == '=' && ac1 != '=' {
				vn := mk(p, "variable_name", s, p.L.b, nil)
				es := p.L.b
				advance(p.L)
				eq := mk(p, "=", es, p.L.b, nil)
				// RHS 可为另一赋值（链式）
				val := parseArithTernary(p, stop, mode)
				end := eq.EndIndex
				kids := []*TsNode{vn, eq}
				if val != nil {
					kids = append(kids, val)
					end = val.EndIndex
				}
				return mk(p, "variable_assignment", s, end, kids)
			}
		}
		// 下标
		if nc == '[' {
			vn := mk(p, "variable_name", s, p.L.b, nil)
			brS := p.L.b
			advance(p.L)
			brOpen := mk(p, "[", brS, p.L.b, nil)
			idx := parseArithTernary(p, "]", "var")
			if idx == nil {
				idx = parseDollarLike(p)
			}
			skipBlanks(p.L)
			var brClose *TsNode
			if peek(p.L, 0) == ']' {
				cs := p.L.b
				advance(p.L)
				brClose = mk(p, "]", cs, p.L.b, nil)
			} else {
				brClose = mk(p, "]", p.L.b, p.L.b, nil)
			}
			kids := []*TsNode{vn, brOpen, brClose}
			if idx != nil {
				kids = []*TsNode{vn, brOpen, idx, brClose}
			}
			return mk(p, "subscript", s, brClose.EndIndex, kids)
		}
		// 裸标识符：'var' 模式为 variable_name，'word'/'assign' 模式为
		// word（'assign' 无 `=` 跟随时落到 word：`c<=5` →
		// binary_expression(word, number)）
		identType := "variable_name"
		if mode != arithModeVar {
			identType = "word"
		}
		return mk(p, identType, s, p.L.b, nil)
	}
	return nil
}

// isArithStop 算术停止点判定（对齐 isArithStop）
func isArithStop(p *ParseState, stop string) bool {
	c := peek(p.L, 0)
	switch stop {
	case "))":
		return c == ')' && peek(p.L, 1) == ')'
	case ")":
		return c == ')'
	case ";":
		return c == ';'
	case ":":
		return c == ':'
	case "]":
		return c == ']'
	case "}":
		return c == '}'
	case ":}":
		return c == ':' || c == '}'
	}
	return c == 0 || c == '\n'
}
