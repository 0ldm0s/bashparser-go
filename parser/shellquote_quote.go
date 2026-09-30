// shellquote_quote.go——npm shell-quote quote 的 Go 直译（quote.js
// 66 行全量语义）。上游包装壳为 eva-cli utils/bash/shellQuote.ts 的
// tryQuoteShellArgs/quote（见 shellquote_wrap.go）。
//
// 语义要点（对齐 quote.js）：
//   - 字符串：空串 → ''；含 " 或空白或反斜杠且不含 ' → 单引号包裹
//     （内部 ' → \'）；含 " ' 或空白 → 双引号包裹（内部 " \ $ ` !
//     → 转义）；其余裸串对 shell 特殊字符逐字转义（/X: 盘符形态的
//     冒号不转义）
//   - glob：GLOB_SHELL_SPECIAL 逐字转义；换行拒收
//   - op：allowlist 查表，逐字符转义；非表内值拒收
//   - comment：'#' + 原文（空串 → '#'）；换行拒收
//   - 未识别 object 形态拒收

package parser

import (
	"fmt"
	"regexp"
	"strings"
)

// QuoteArgKind 参数形态（对齐 quote.js map 回调的联合输入）。
type QuoteArgKind int

const (
	QuoteStr         QuoteArgKind = iota // 字符串参数
	QuoteGlob                            // {op:'glob',pattern:...}
	QuoteOp                              // {op:...}
	QuoteComment                         // {comment:...}
	QuoteUnknownObj                      // 未识别 object（恒拒收）
)

// QuoteArg 单个引号参数。
type QuoteArg struct {
	Kind    QuoteArgKind
	Str     string // QuoteStr：参数原文
	Pattern string // QuoteGlob：glob 模式
	Op      string // QuoteOp：操作符字面量
	Comment string // QuoteComment：注释原文（可为空串）
}

var (
	// 换行终结符全集（对齐 /[\n\r\u2028\u2029]/——RE2 用 \x{} 表达
	// U+2028/U+2029）
	sqQuoteLineTerminators = regexp.MustCompile(`[\n\r\x{2028}\x{2029}]`)
	sqQuoteGlobSpecial     = regexp.MustCompile(`[\s#!"$&'():;<=>@\\^` + "`" + `|]`)
	sqQuoteNeedsSingle     = regexp.MustCompile(`["\s\\]`)
	sqQuoteHasSingle       = regexp.MustCompile(`'`)
	sqQuoteNeedsDouble     = regexp.MustCompile(`["'\s]`)
	sqQuoteDoubleEscape    = regexp.MustCompile(`(["\\$` + "`" + `!])`)
	sqQuoteBareEscape      = regexp.MustCompile(`([A-Za-z]:)?([#!"$&'()*,:;<=>?@[\\\]^\` + "`" + `{|}~])`)
	sqQuoteOps             = map[string]bool{
		"||": true, "&&": true, ";;": true, "|&": true, "<(": true,
		"<<<": true, ">>": true, ">&": true, "<&": true, "&": true,
		";": true, "(": true, ")": true, "|": true, "<": true, ">": true,
	}
)

// ShellQuoteQuote 引号化参数序列（空格连接；对齐 quote.js 主体）。
// 非法输入返回错误（调用方决定降级形态）。
func ShellQuoteQuote(args []QuoteArg) (string, error) {
	parts := make([]string, 0, len(args))
	for _, s := range args {
		q, err := sqQuoteOne(s)
		if err != nil {
			return "", err
		}
		parts = append(parts, q)
	}
	return strings.Join(parts, " "), nil
}

// sqQuoteOne 单参数引号化（对齐 quote.js 28-64 map 回调）。
func sqQuoteOne(s QuoteArg) (string, error) {
	switch s.Kind {
	case QuoteStr:
		if s.Str == "" {
			return `''`, nil
		}
		if sqQuoteNeedsSingle.MatchString(s.Str) && !sqQuoteHasSingle.MatchString(s.Str) {
			return `'` + strings.ReplaceAll(s.Str, `'`, `\'`) + `'`, nil
		}
		if sqQuoteNeedsDouble.MatchString(s.Str) {
			inner := sqQuoteDoubleEscape.ReplaceAllString(s.Str, `\$1`)
			return `"` + inner + `"`, nil
		}
		// 裸串：shell 特殊字符转义（/X: 盘符形态的冒号不转义）
		return sqQuoteBareEscape.ReplaceAllString(s.Str, `$1\$2`), nil

	case QuoteGlob:
		if sqQuoteLineTerminators.MatchString(s.Pattern) {
			return "", fmt.Errorf("glob `pattern` must not contain line terminators")
		}
		return sqQuoteGlobSpecial.ReplaceAllString(s.Pattern, `\$0`), nil

	case QuoteOp:
		if !sqQuoteOps[s.Op] {
			return "", fmt.Errorf("invalid `op` value: %s", s.Op)
		}
		var b strings.Builder
		for _, r := range s.Op {
			b.WriteByte('\\')
			b.WriteRune(r)
		}
		return b.String(), nil

	case QuoteComment:
		if sqQuoteLineTerminators.MatchString(s.Comment) {
			return "", fmt.Errorf("`comment` must not contain line terminators")
		}
		return "#" + s.Comment, nil

	default:
		return "", fmt.Errorf("unrecognized object token shape")
	}
}
