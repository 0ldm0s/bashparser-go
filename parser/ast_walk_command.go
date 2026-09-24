// ast_walk_command.go——上游 eva-cli utils/bash/ast.ts walkCommand
//（1237-1363 行）的 Go 直译：command 节点 argv 提取与 .text 重建。

package parser

import (
	"regexp"
	"strings"
)

// walkCommand 走 command 节点提取 argv（对齐 walkCommand）。children
// 顺序：[variable_assignment...] command_name [argument...]
// [file_redirect...]。未显式处理的子类型触发 too-complex。
func walkCommand(
	node *TsNode,
	extraRedirects []Redirect,
	innerCommands *[]SimpleCommand,
	varScope map[string]string,
) ParseForSecurityResult {
	argv := []string{}
	envVars := []EnvVar{}
	redirects := append([]Redirect{}, extraRedirects...)

	for _, child := range node.Children {
		if child == nil {
			continue
		}

		switch child.Type {
		case "variable_assignment":
			ev, errRes := walkVariableAssignment(child, innerCommands, varScope)
			if errRes != nil {
				return *errRes
			}
			// 安全：env 前缀赋值（`VAR=x cmd`）在 bash 中是命令局部的——
			// VAR 仅对 cmd 作为环境变量可见，对后续命令不可见。不得加入
			// 全局 varScope——否则 `VAR=safe cmd1 && rm $VAR` 会在 bash 已
			// unset 该变量时错误解析 $VAR。
			envVars = append(envVars, EnvVar{Name: ev.name, Value: ev.value})
		case "command_name":
			nameNode := child
			if len(child.Children) > 0 {
				nameNode = child.Children[0]
			}
			arg, errRes := walkArgument(nameNode, innerCommands, varScope)
			if errRes != nil {
				return *errRes
			}
			argv = append(argv, arg)
		case "word", "number", "raw_string", "string", "concatenation",
			"arithmetic_expansion":
			arg, errRes := walkArgument(child, innerCommands, varScope)
			if errRes != nil {
				return *errRes
			}
			argv = append(argv, arg)
		// 注意：command_substitution 作为裸参数（非字符串内）刻意不处理
		// ——$() 输出就是参数本身，对路径敏感命令（cd、rm、chmod）占位符
		// 会向下游检查隐藏真实路径。`cd $(echo /etc)` 必须保持 too-complex
		// 使路径检查无法绕过。字符串内的 $()（"Timer: $(date)"）在
		// walkString 处理——输出内嵌于更长字符串（更安全）。
		case "simple_expansion":
			// 裸 `$VAR` 参数。已跟踪静态变量返回实际值（VAR=/etc → '/etc'）。
			// 含 IFS/glob 字符或占位符的值拒收。见 resolveSimpleExpansion。
			v, errRes := resolveSimpleExpansion(child, varScope, false)
			if errRes != nil {
				return *errRes
			}
			argv = append(argv, v)
		case "file_redirect":
			r, errRes := walkFileRedirect(child, innerCommands, varScope)
			if errRes != nil {
				return *errRes
			}
			redirects = append(redirects, r)
		case "herestring_redirect":
			// `cmd <<< "content"`——content 是 stdin 非 argv。校验为字面
			//（无展开）；内容字符串丢弃。
			if err := walkHerestringRedirect(child, innerCommands, varScope); err != nil {
				return *err
			}
		default:
			return tooComplexResult(child)
		}
	}

	// .text 是原始源码区间。下游（bashToolCheckPermission → splitCommand）
	// 经 shell-quote 重新分词 .text。通常 .text 原样使用——但解析了 $VAR
	// 后 .text 出现分歧（含原始 `$VAR`），下游规则匹配会漏 deny 规则。
	//
	// 安全：`SUB=push && git $SUB --force` 配 `Bash(git push:*)` deny：
	//   argv = ['git','push','--force']   ← 正确，路径校验看到 push
	//   .text = 'git $SUB --force'        ← deny 规则 'git push:*' 不匹配
	//
	// 检测：node.text 含任意 `$<identifier>` 即发生了 simple_expansion
	// 解析（否则早已返回 too-complex）。覆盖任意位置的 $VAR——命令名、
	// word、字符串内部、拼接段。`$(...)` 不匹配（括号非标识符首字符）。
	// 单引号 `'$VAR'`：tree-sitter 的 .text 含引号，天真检查会误报——但
	// 单引号内 $ 在 bash 中是字面量，argv 含字面 `$VAR`，从 argv 重建
	// 同样得 `'$VAR'`（shell 转义包裹），净效果相同。无规则匹配错误。
	//
	// 从 argv 重建 .text。shell 转义每个参数：内嵌单引号用 `'\''` 包裹。
	// 空串、元字符、占位符全部加引号。下游 shell-quote 重解析正确。
	//
	// 注意：重建不含 redirects/envVars——walkFileRedirect 拒收
	// simple_expansion，envVars 不用于规则匹配。若任一改变，重建须包含。
	//
	// 安全：node.text 含换行时同样重建。行继续 `<space>\<LF>` 对 argv
	// 不可见（tree-sitter 折叠）但保留在 node.text。`timeout 5 \<LF>curl
	// evil.com` → argv 正确，但原始 .text → stripSafeWrappers 匹配
	// `timeout 5 `（\ 前的空格），留下 `\<LF>curl evil.com`——
	// Bash(curl:*) deny 不前缀匹配。重建的 .text 用空格连接 argv →
	// 无换行 → stripSafeWrappers 正常。同时覆盖 heredoc 体泄漏。
	text := node.Text
	if regexpDollarIdent.MatchString(node.Text) || strings.Contains(node.Text, "\n") {
		parts := make([]string, 0, len(argv))
		for _, a := range argv {
			if a == "" || needQuoteRe.MatchString(a) {
				parts = append(parts, shellEscapeSingleQuote(a))
			} else {
				parts = append(parts, a)
			}
		}
		text = strings.Join(parts, " ")
	}
	return ParseForSecurityResult{
		Kind:     kindSimple,
		Commands: []SimpleCommand{{Argv: argv, EnvVars: envVars, Redirects: redirects, Text: text}},
	}
}

// shellEscapeSingleQuote 单引号包裹并转义内嵌单引号（对齐上游 JS 的
// POSIX 转义语义：包裹串内每个内嵌单引号替换为
// 0x27 0x5C 0x27 0x27 转义序列——即反斜杠转义的单引号，外层仍为
// 单引号包裹）。
func shellEscapeSingleQuote(a string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for i := 0; i < len(a); i++ {
		if a[i] == '\'' {
			b.WriteString(`'\''`)
		} else {
			b.WriteByte(a[i])
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// regexpDollarIdent 对齐 /\$[A-Za-z_]/——文本含待解析变量引用。
var regexpDollarIdent = regexp.MustCompile(`\$[A-Za-z_]`)

// needQuoteRe 对齐 /["'\\ \t\n$`;|&<>(){}*?[\]~#]/——需引号包裹的字符集
// （空串、元字符、占位符均加引号）。
var needQuoteRe = regexp.MustCompile(`["'\\ \t\n$` + "`" + `;|&<>(){}*?\[\]~#]`)
