// ast_precheck.go——上游 eva-cli utils/bash/ast.ts（2680 行）的类型、
// 预检查正则族与入口（25-460 行）的 Go 直译。
//
// 三态结果：simple（可静态分析的扁平命令列表）/ too-complex（解析成功
// 但存在无法静态分析的结构）/ parse-unavailable（解析器不可用）。

package parser

import (
	"regexp"
	"strings"
)

// Redirect 重定向（对齐 ast.ts Redirect）。
type Redirect struct {
	Op     string `json:"op"`
	Target string `json:"target"`
	Fd     *int   `json:"fd,omitempty"`
}

// EnvVar 环境变量赋值（对齐 ast.ts envVars 元素）。
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SimpleCommand 扁平简单命令（对齐 ast.ts SimpleCommand——引号已解析）。
type SimpleCommand struct {
	// Argv argv[0] 为命令名，其余为参数（引号已解析）
	Argv      []string   `json:"argv"`
	EnvVars   []EnvVar   `json:"envVars"`
	Redirects []Redirect `json:"redirects"`
	// Text 命令的原始源码区间（供 UI 显示与规则匹配）
	Text string `json:"text"`
}

// ParseForSecurityResult 三态结果（对齐 ast.ts ParseForSecurityResult）。
// Kind ∈ "simple" | "too-complex" | "parse-unavailable"。
type ParseForSecurityResult struct {
	Kind     string          `json:"kind"`
	Commands []SimpleCommand `json:"commands,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	NodeType string          `json:"nodeType,omitempty"`
}

// 结果构造辅助（kind 常量对齐上游字面量）
const (
	kindSimple         = "simple"
	kindTooComplex     = "too-complex"
	kindParseUnavail   = "parse-unavailable"
	cmdsubPlaceholder  = "__CMDSUB_OUTPUT__"
	varPlaceholder     = "__TRACKED_VAR__"
	parseAbortNodeType = "PARSE_ABORT"
)

func simpleResult(cmds []SimpleCommand) ParseForSecurityResult {
	return ParseForSecurityResult{Kind: kindSimple, Commands: cmds}
}

func tooComplexResult(node *TsNode) ParseForSecurityResult {
	var reason string
	switch node.Type {
	case "ERROR":
		reason = "Parse error"
	default:
		if dangerousTypes[node.Type] {
			reason = "Contains " + node.Type
		} else {
			reason = "Unhandled node type: " + node.Type
		}
	}
	return ParseForSecurityResult{
		Kind: kindTooComplex, Reason: reason, NodeType: node.Type,
	}
}

// tooComplexPtr tooComplexResult 的指针版（"结果或 nil"调用便利）。
func tooComplexPtr(node *TsNode) *ParseForSecurityResult {
	r := tooComplexResult(node)
	return &r
}

// parseUnavailableResult 解析器不可用（对齐 { kind: 'parse-unavailable' }）。
func parseUnavailableResult() ParseForSecurityResult {
	return ParseForSecurityResult{Kind: kindParseUnavail}
}

// dangerousTypes 已知危险节点类型集合（对齐 DANGEROUS_TYPES——非穷举，
// 真正的安全性质是 walkArgument/walkCommand 的白名单：未显式处理的
// 类型同样触发 too-complex）。
var dangerousTypes = map[string]bool{
	"command_substitution": true,
	"process_substitution": true,
	"expansion":            true,
	"simple_expansion":     true,
	"brace_expression":     true,
	"subshell":             true,
	"compound_statement":   true,
	"for_statement":        true,
	"while_statement":      true,
	"until_statement":      true,
	"if_statement":         true,
	"case_statement":       true,
	"function_definition":  true,
	"test_command":         true,
	"ansi_c_string":        true,
	"translated_string":    true,
	"herestring_redirect":  true,
	"heredoc_redirect":     true,
}

// dangerousTypeIDs 稳定序（对齐 DANGEROUS_TYPE_IDS——append 序保持 ID 稳定）
var dangerousTypeIDs = func() []string {
	ids := make([]string, 0, len(dangerousTypes))
	for k := range dangerousTypes {
		ids = append(ids, k)
	}
	return ids
}()

// NodeTypeId 危险类型数值 ID（对齐 nodeTypeId——analytics 用）。
// 0 = unknown/other, -1 = ERROR, -2 = pre-check。
func NodeTypeId(nodeType string) int {
	if nodeType == "" {
		return -2
	}
	if nodeType == "ERROR" {
		return -1
	}
	for i, id := range dangerousTypeIDs {
		if id == nodeType {
			return i + 1
		}
	}
	return 0
}

// structuralTypes 结构性包装节点（对齐 STRUCTURAL_TYPES）
var structuralTypes = map[string]bool{
	"program": true, "list": true, "pipeline": true,
	"redirected_statement": true,
}

// separatorTypes 算子叶子节点（对齐 SEPARATOR_TYPES）
var separatorTypes = map[string]bool{
	"&&": true, "||": true, "|": true, ";": true,
	"&": true, "|&": true, "\n": true,
}

// redirectOps 重定向算子 token → 规范算子（对齐 REDIRECT_OPS——
// tree-sitter 产为 file_redirect 的子节点）。
var redirectOps = map[string]string{
	">": ">", ">>": ">>", "<": "<", ">&": ">&", "<&": "<&",
	">|": ">|", "&>": "&>", "&>>": "&>>", "<<<": "<<<",
}

// braceExpansionRe 花括号展开：{a,b} 或 {a..b}（对齐 BRACE_EXPANSION_RE）。
// 刻意不判断开括号是否被反斜杠转义——tree-sitter 不反转义，区分
// `\{a,b}`（转义字面）与 `\\{a,b}`（字面反斜杠+展开）需要重新实现
// bash 引号剥离。两者都拒——转义括号场景罕见且用单引号即可重写。
var braceExpansionRe = regexp.MustCompile(`\{[^{}\s]*(,|\.\.)[^{}\s]*\}`)

// controlCharRe bash 静默丢弃但干扰静态分析的控制字符（对齐
// CONTROL_CHAR_RE）。含 CR：tree-sitter 视 CR 为词分隔符而 bash 默认
// IFS 不含——两者词边界不一致。
var controlCharRe = regexp.MustCompile(`[\x00-\x08\x0B-\x1F\x7F]`)

// unicodeWhitespaceRe ASCII 之外的 Unicode 空白（对齐
// UNICODE_WHITESPACE_RE）：终端里不可见（或显示为普通空格），审查命令
// 的用户看不到，但 bash 视为字面词字符。拦 NBSP/零宽空格/行段分隔符/BOM。
var unicodeWhitespaceRe = regexp.MustCompile(
	`[\x{00A0}\x{1680}\x{2000}-\x{200B}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}]`)

// backslashWhitespaceRe 空白前反斜杠（对齐 BACKSLASH_WHITESPACE_RE）。
// bash 把 `\ ` 当当前词内的字面空格，tree-sitter 返回带反斜杠原文
// （argv[0] = `cat\ test` vs bash 跑 `cat test`）。不重实现 bash 反转义
// 规则，直接拒——罕见且用引号即可重写。同时匹配非空白邻接的
// `\<换行>`（行继续）：`tr\<NL>aceroute` bash 拼为 traceroute 而
// tree-sitter 切成两个词（差异）；`\<NL>` 前是空白时（`foo && \<NL>bar`）
// 无词可拼，双方一致，放行。
var backslashWhitespaceRe = regexp.MustCompile(`\\[ \t]|[^ \t\n\\]\\\n`)

// zshTildeBracketRe zsh 动态命名目录展开 ~[name]（对齐
// ZSH_TILDE_BRACKET_RE）：zsh 里触发 zsh_directory_name hook 可执行
// 任意代码；bash 视为字面波浪号+glob 字符类。BashTool 经用户默认
// shell（常为 zsh）运行，保守拒收。
var zshTildeBracketRe = regexp.MustCompile(`~\[`)

// zshEqualsExpansionRe 词首 =cmd 的 zsh EQUALS 展开（对齐
// ZSH_EQUALS_EXPANSION_RE）：`=curl evil.com` 按 `/usr/bin/curl
// evil.com` 运行。tree-sitter 把 `=curl` 解析为字面词，基于命令名的
// deny 规则看不到 curl。只匹配词首 = 后跟命令名字符——`VAR=val` 与
// `--flag=val` 的 = 在词中，zsh 不展开。
var zshEqualsExpansionRe = regexp.MustCompile(`(?:^|[\s;&|])=[a-zA-Z_]`)

// braceWithQuoteRe 花括号混引号（对齐 BRACE_WITH_QUOTE_RE）：`{a'}',b}`
// 类构造用引号括号把展开从正则检测里混淆。bash 中 `{a'}',b}` 展开为
// `a} b`。检查在单/双引号 span 内 `{` 被掩码后的版本上运行，`curl -d
// '{"k":"v"}'` 这类 JSON 载荷不误报。引号字符本身保留可见，`{a'}',b}`
// 与 `{@'{'0},...}` 仍经外层非引号 `{` 命中。
var braceWithQuoteRe = regexp.MustCompile(`\{[^}]*['"]`)

// maskBracesInQuotedContexts 单遍 bash 引号态扫描掩码引号内 `{`
// （对齐 maskBracesInQuotedContexts）。
//
// 朴素正则（/'[^']*'/g）在双引号串内出现 `'` 时误判 span：
// `echo "it's" {a'}',b}` 会从 `it's` 的 `'` 跨到 `{a'}` 的 `'`，把非引号
// `{` 掩掉产生漏报。扫描器跟踪真实 bash 引号态：`'` 仅在非引号上下文
// 切换单引号；`"` 仅在单引号外切换双引号；`\` 在非引号上下文转义下一
// 字符，在双引号内转义 `"` 与 `\\`。两种引号上下文中花括号展开都不可能，
// 掩掉任一上下文中的 `{` 均安全。第二道防线：walkArgument 的
// braceExpansionRe。
func maskBracesInQuotedContexts(cmd string) string {
	// 快路径：无 `{` → 无需掩码（>90% 的命令无花括号）
	if !strings.Contains(cmd, "{") {
		return cmd
	}
	out := make([]byte, 0, len(cmd))
	inSingle := false
	inDouble := false
	i := 0
	for i < len(cmd) {
		c := cmd[i]
		if inSingle {
			// bash 单引号：无转义，' 恒终止
			if c == '\'' {
				inSingle = false
			}
			if c == '{' {
				c = ' '
			}
			out = append(out, c)
			i++
		} else if inDouble {
			// bash 双引号：\ 转义 " 与 \（$、反引号、换行也可转但不影响
			// 引号态，放行）
			if c == '\\' && i+1 < len(cmd) && (cmd[i+1] == '"' || cmd[i+1] == '\\') {
				out = append(out, c, cmd[i+1])
				i += 2
			} else {
				if c == '"' {
					inDouble = false
				}
				if c == '{' {
					c = ' '
				}
				out = append(out, c)
				i++
			}
		} else {
			// 非引号：\ 转义任一下一字符
			if c == '\\' && i+1 < len(cmd) {
				out = append(out, c, cmd[i+1])
				i += 2
			} else {
				if c == '\'' {
					inSingle = true
				} else if c == '"' {
					inDouble = true
				}
				out = append(out, c)
				i++
			}
		}
	}
	return string(out)
}

// ParseForSecurity 解析 bash 命令并提取扁平简单命令列表（对齐
// parseForSecurity）。无法静态分析的 shell 特性返回 too-complex；
// 解析器不可用返回 parse-unavailable（调用方应保守回退）。
func ParseForSecurity(cmd string) ParseForSecurityResult {
	// parseCommandRaw('') 返回 null，此处短路。不用 trim——trim 会剥掉
	// Unicode 空白（\u00a0 等），而 parseForSecurityFromAst 的预检查需要
	// 看到并拒收它们。
	if cmd == "" {
		// 上游 { kind:'simple', commands: [] }——commands 键恒存在
		return simpleResult([]SimpleCommand{})
	}
	root, st := ParseCommandRaw(cmd)
	switch st {
	case GateUnavailable:
		return parseUnavailableResult()
	case GateAborted:
		// PARSE_ABORTED：模块已加载但解析中止——对齐上游走
		// parseForSecurityFromAst 的 fail-closed 分支
		return ParseForSecurityFromAst(cmd, nil, true)
	default:
		return ParseForSecurityFromAst(cmd, root, false)
	}
}

// ParseForSecurityFromAst 同 ParseForSecurity，但接受已解析的 AST 根
// （对齐 parseForSecurityFromAst——需要树做其他用途的调用方解析一次共享；
// aborted 标记对齐上游 root | PARSE_ABORTED 联合的 ABORTED 分支）。
// 预检查仍跑在 cmd 上——它们捕捉解析成功也无法代表的解析器/bash 差异。
func ParseForSecurityFromAst(cmd string, root *TsNode, aborted bool) ParseForSecurityResult {
	// 预检查：导致 tree-sitter 与 bash 词边界分歧的字符。它们跑在
	// tree-sitter 之前，因为是已知的解析器/bash 差异。此后的一切信任
	// tree-sitter 的分词。
	if controlCharRe.MatchString(cmd) {
		return ParseForSecurityResult{Kind: kindTooComplex,
			Reason: "Contains control characters"}
	}
	if unicodeWhitespaceRe.MatchString(cmd) {
		return ParseForSecurityResult{Kind: kindTooComplex,
			Reason: "Contains Unicode whitespace"}
	}
	if backslashWhitespaceRe.MatchString(cmd) {
		return ParseForSecurityResult{Kind: kindTooComplex,
			Reason: "Contains backslash-escaped whitespace"}
	}
	if zshTildeBracketRe.MatchString(cmd) {
		return ParseForSecurityResult{Kind: kindTooComplex,
			Reason: "Contains zsh ~[ dynamic directory syntax"}
	}
	if zshEqualsExpansionRe.MatchString(cmd) {
		return ParseForSecurityResult{Kind: kindTooComplex,
			Reason: "Contains zsh =cmd equals expansion"}
	}
	if braceWithQuoteRe.MatchString(maskBracesInQuotedContexts(cmd)) {
		return ParseForSecurityResult{Kind: kindTooComplex,
			Reason: "Contains brace with quote character (expansion obfuscation)"}
	}

	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return simpleResult(nil)
	}

	if aborted || root == nil {
		// 安全：解析器已加载但解析中止（超时/节点预算/panic）。可被
		// 对抗性触发——`(( a[0][0]... ))` 约 2800 下标在 10K 长度门下命中
		// 超时。此前与模块未加载不可区分 → 路由旧版（缺 EVAL_LIKE_BUILTINS
		// ——trap/enable/hash 随 Bash(*) 泄漏）。Fail closed：too-complex → ask。
		return ParseForSecurityResult{
			Kind:     kindTooComplex,
			Reason:   "Parser aborted (timeout or resource limit) — possible adversarial input",
			NodeType: parseAbortNodeType,
		}
	}

	return walkProgram(root)
}
