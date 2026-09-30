// shellquote_wrap.go——eva-cli utils/bash/shellQuote.ts 五函数的 Go
// 直译（shell-quote 库的安全包装壳）：
//   - TryParseShellCommand（24-45）：ShellQuoteParse 的错误安全包装
//   - TryQuoteShellArgs（47-95）：ShellQuoteQuote 的错误安全包装
//   - HasMalformedTokens（117-175）：标记级畸形检测（括号/引号平衡）
//   - HasShellQuoteSingleQuoteBug（189-263）：单引号内反斜杠差分检测
//     （H1 #3482049 命令注入防线）
//   - QuoteArgs（265-302）：严格校验 + 宽松回退的 quote 入口

package parser

import (
	"strings"
)

// ShellParseResult 解析结果（对齐 ShellParseResult 联合——success 时
// tokens；失败时 error 文本）。
type ShellParseResult struct {
	Success bool
	Tokens  []ParseToken
	Error   string
}

// TryParseShellCommand 解析命令为 token（解析失败不 panic——返回
// success=false；对齐 tryParseShellCommand）。env 形态见 EnvFunc。
func TryParseShellCommand(cmd string, env EnvFunc) ShellParseResult {
	tokens, err := ShellQuoteParse(cmd, env)
	if err != nil {
		return ShellParseResult{Success: false, Error: err.Error()}
	}
	return ShellParseResult{Success: true, Tokens: tokens}
}

// ShellQuoteResult 引号化结果（对齐 ShellQuoteResult 联合）。
type ShellQuoteResult struct {
	Success bool
	Quoted  string
	Error   string
}

// TryQuoteShellArgs 严格引号化：字符串参数逐个引号化；非字符串形态
// 直接拒收（对齐 tryQuoteShellArgs 的类型校验——Go 侧 QuoteArg 由调
// 用方构造，非 QuoteStr 形态即拒收）。
func TryQuoteShellArgs(args []QuoteArg) ShellQuoteResult {
	for i, a := range args {
		if a.Kind != QuoteStr {
			return ShellQuoteResult{Success: false,
				Error: "无法对索引 " + sqItoa(i) + " 处的参数加引号：不支持非字符串值"}
		}
	}
	quoted, err := ShellQuoteQuote(args)
	if err != nil {
		return ShellQuoteResult{Success: false, Error: err.Error()}
	}
	return ShellQuoteResult{Success: true, Quoted: quoted}
}

// QuoteArgs 宽松 quote 入口（对齐 quote 265-302）：先严格校验；失败
// 时全部转字符串后重试（Go 侧非字符串形态由调用方先行转换，此处仅
// 保留严格路径 + 未识别 object 的拒收错误透传）。
func QuoteArgs(args []QuoteArg) (string, error) {
	result := TryQuoteShellArgs(args)
	if result.Success {
		return result.Quoted, nil
	}
	// 宽松回退：字符串化所有参数（Go 侧 QuoteArg 非 QuoteStr 形态取
	// 其字面承载字段——对应上游 String(arg)/jsonStringify(arg) 合流）
	stringArgs := make([]QuoteArg, 0, len(args))
	for _, a := range args {
		switch a.Kind {
		case QuoteStr:
			stringArgs = append(stringArgs, a)
		case QuoteGlob:
			stringArgs = append(stringArgs, QuoteArg{Kind: QuoteStr, Str: a.Pattern})
		case QuoteOp:
			stringArgs = append(stringArgs, QuoteArg{Kind: QuoteStr, Str: a.Op})
		case QuoteComment:
			stringArgs = append(stringArgs, QuoteArg{Kind: QuoteStr, Str: "#" + a.Comment})
		default:
			return "", errQuoteUnsafe
		}
	}
	quoted, err := ShellQuoteQuote(stringArgs)
	if err != nil {
		return "", errQuoteUnsafe
	}
	return quoted, nil
}

// errQuoteUnsafe 安全引号化最终失败（对齐 "Failed to quote shell
// arguments safely"——JSON.stringify 形态不作回退：双引号不阻止
// shell 执行）
var errQuoteUnsafe = quoteError("无法安全引号化 shell 参数")

type quoteError string

func (e quoteError) Error() string { return string(e) }

// HasMalformedTokens 标记级畸形检测（对齐 hasMalformedTokens 117-175）：
// ①原命令 bash 语义遍历——未终止引号（奇数个）即畸形（shell-quote 会
// 静默丢弃不匹配引号不留痕）；②逐 string token 检查花括号/圆括号/
// 方括号平衡与未转义引号奇偶——类 JSON 输入（如 {"hi":"hi;evil"}）被
// shell-quote 按 shell 规则拆出 `;` 运算符时产生碎片 token。
func HasMalformedTokens(command string, parsed []ParseToken) bool {
	// 原命令引号遍历：反斜杠在单引号外转义下一字符；单引号内无转义
	inSingle, inDouble := false, false
	doubleCount, singleCount := 0, 0
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == '\\' && !inSingle {
			i++
			continue
		}
		if c == '"' && !inSingle {
			doubleCount++
			inDouble = !inDouble
		} else if c == '\'' && !inDouble {
			singleCount++
			inSingle = !inSingle
		}
	}
	if doubleCount%2 != 0 || singleCount%2 != 0 {
		return true
	}

	for _, entry := range parsed {
		if entry.Kind != TokenKindStr {
			continue
		}
		s := entry.Str
		if strings.Count(s, "{") != strings.Count(s, "}") {
			return true
		}
		if strings.Count(s, "(") != strings.Count(s, ")") {
			return true
		}
		if strings.Count(s, "[") != strings.Count(s, "]") {
			return true
		}
		// 未转义引号奇偶（对齐 (?<!\\)" lookbehind——手工遍历）
		if countUnescaped(s, '"')%2 != 0 {
			return true
		}
		if countUnescaped(s, '\'')%2 != 0 {
			return true
		}
	}
	return false
}

// countUnescaped 统计未转义出现次数（前导字符非反斜杠；串首视为未
// 转义——对齐 JS (?<!\\) 语义）
func countUnescaped(s string, target rune) int {
	runes := []rune(s)
	count := 0
	for i, r := range runes {
		if r != target {
			continue
		}
		if i > 0 && runes[i-1] == '\\' {
			continue
		}
		count++
	}
	return count
}

// HasShellQuoteSingleQuoteBug 单引号内反斜杠差分检测（对齐
// hasShellQuoteSingleQuoteBug 189-263）：bash 中单引号内反斜杠为字
// 面（'\' 即 字符串\），shell-quote 却把 \' 视为转义——`'\<payload>\'`
// 将 payload 藏进"未闭合"单引号串绕过检查。
//
// 判定：bash 语义遍历中刚关闭一个单引号时，数其前导反斜杠——
//   - 奇数个：恒为 bug（shell-quote 消费了本应闭合的引号）
//   - 偶数个：仅当命令中还存在后续 '（chunker 可借用的假闭合位）
//     时为 bug
func HasShellQuoteSingleQuoteBug(command string) bool {
	inSingleQuote, inDoubleQuote := false, false
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		char := runes[i]

		// 单引号外反斜杠转义下一字符
		if char == '\\' && !inSingleQuote {
			i++
			continue
		}
		if char == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			continue
		}
		if char == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote

			// 刚关闭单引号：检查前导反斜杠（上游注释——chunker 正则
			// 的 \' 替代分支优先于 [^']，差异形态见上游 H1 报告样例：
			// git ls-remote 'safe\\' '--upload-pack=evil' 'repo'）
			if !inSingleQuote {
				backslashCount := 0
				for j := i - 1; j >= 0 && runes[j] == '\\'; j-- {
					backslashCount++
				}
				if backslashCount > 0 && backslashCount%2 == 1 {
					return true
				}
				// 偶数个尾随反斜杠：仅当后续还存在 ' 时为 bug
				// （chunker 正则不尊重 bash 引号态——双引号内的 ' 亦
				// 可被消费）
				if backslashCount > 0 && backslashCount%2 == 0 &&
					strings.Contains(string(runes[i+1:]), `'`) {
					return true
				}
			}
			continue
		}
	}
	return false
}

// sqItoa 极简整数转十进制字符串（包装层错误消息用）
func sqItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
