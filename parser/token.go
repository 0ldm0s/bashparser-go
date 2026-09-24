package parser

import "strings"

// token.go：token 类型与词法常量集合（对齐 bashParser.ts 12-103 行）。

// TokenType token 类型常量（对齐上游 TokenType 联合）
const (
	TokWord         = "WORD"
	TokNumber       = "NUMBER"
	TokOp           = "OP"
	TokNewline      = "NEWLINE"
	TokComment      = "COMMENT"
	TokDquote       = "DQUOTE"
	TokSquote       = "SQUOTE"
	TokAnsiC        = "ANSI_C"
	TokDollar       = "DOLLAR"
	TokDollarParen  = "DOLLAR_PAREN"
	TokDollarBrace  = "DOLLAR_BRACE"
	TokDollarDParen = "DOLLAR_DPAREN"
	TokBacktick     = "BACKTICK"
	TokLtParen      = "LT_PAREN"
	TokGtParen      = "GT_PAREN"
	TokEOF          = "EOF"
)

// Token 单个词法单元；Start/End 为 UTF-8 字节偏移
type Token struct {
	Type  string
	Value string
	Start int
	End   int
}

// specialVars 特殊变量字符（对齐 SPECIAL_VARS）
var specialVars = map[rune]bool{
	'?': true, '$': true, '@': true, '*': true,
	'#': true, '-': true, '!': true, '_': true,
}

// declKeywords 声明类命令（对齐 DECL_KEYWORDS）
var declKeywords = map[string]bool{
	"export": true, "declare": true, "typeset": true,
	"readonly": true, "local": true,
}

// shellKeywords shell 关键字（对齐 SHELL_KEYWORDS）
var shellKeywords = map[string]bool{
	"if": true, "then": true, "elif": true, "else": true, "fi": true,
	"while": true, "until": true, "for": true, "in": true, "do": true,
	"done": true, "case": true, "esac": true, "function": true,
	"select": true,
}

// statementBreakKeywords 语句流断点关键字（then/elif/else/fi/do/done/esac）
var statementBreakKeywords = map[string]bool{
	"then": true, "elif": true, "else": true, "fi": true,
	"do": true, "done": true, "esac": true,
}

// isAllDigitsSigned 对齐上游正则 /^-?\d+$/（NUMBER 判定）
func isAllDigitsSigned(v string) bool {
	s := strings.TrimPrefix(v, "-")
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isAllDigits 对齐正则 /^\d+$/
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isBlankOnly 对齐正则 /^[ \t]+$/
func isBlankOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != ' ' && r != '\t' {
			return false
		}
	}
	return true
}
