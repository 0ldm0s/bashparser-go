// ast_walk_arg.go——上游 eva-cli utils/bash/ast.ts 的参数与字符串处理
//（1399-1652 walkArgument/walkString、1937-2031
// resolveSimpleExpansion/stripRawString）的 Go 直译。

package parser

import (
	"regexp"
	"strings"
)

// bareVarUnsafeRe 裸 $VAR 展开的危险值字符（对齐 BARE_VAR_UNSAFE_RE）。
// 非引号 $VAR 经历词分割（$IFS：空格/tab/NL）与路径名展开（glob：
// * ? [）。值含这些元字符时不可信为裸参数：`VAR="-rf /" && rm $VAR` →
// bash 跑 `rm -rf /`（两参数）而 argv 是 ['rm','-rf /']（一参数）。
// 双引号内（"$VAR"）分割与 glob 均不适用——值就是单个字面参数。
var bareVarUnsafeRe = regexp.MustCompile(`[ \t\n*?[]`)

// walkArgument 参数节点转字面字符串值（对齐 walkArgument——引号解析，
// 实现参数位置白名单）。
func walkArgument(
	node *TsNode,
	innerCommands *[]SimpleCommand,
	varScope map[string]string,
) (string, *ParseForSecurityResult) {
	if node == nil {
		return "", &ParseForSecurityResult{Kind: kindTooComplex,
			Reason: "Null argument node"}
	}

	switch node.Type {
	case "word":
		// 反斜杠序列反转义。非引号上下文 bash 引号剥离把 `\X` → X。
		// tree-sitter 保留原文。checkSemantics 需要：`\eval` 必须命中
		// EVAL_LIKE_BUILTINS、`\zmodload` 命中 ZSH_DANGEROUS_BUILTINS。
		// 也使 argv 准确：`find -exec {} \;` → argv 是 `;` 非 `\;`
		//（.text 上的 deny 匹配已经由下游 splitCommand 反转义覆盖）。
		// `\<空白>` 已被 BACKSLASH_WHITESPACE_RE 拒收。
		if braceExpansionRe.MatchString(node.Text) {
			return "", &ParseForSecurityResult{Kind: kindTooComplex,
				Reason:   "Word contains brace expansion syntax",
				NodeType: "word"}
		}
		return unescapeBackslashes(node.Text), nil

	case "number":
		// 安全：tree-sitter-bash 把 `NN#<expansion>`（算术基底语法）解析为
		// 带 expansion 子节点的 number 节点。`10#$(cmd)` 的 .text 是完整
		// 字面但子节点是 command_substitution——bash 会执行，展开会走私
		// 过权限检查。纯数字（10、16#ff）无子节点。
		if len(node.Children) > 0 {
			childType := ""
			if node.Children[0] != nil {
				childType = node.Children[0].Type
			}
			return "", &ParseForSecurityResult{Kind: kindTooComplex,
				Reason:   "Number node contains expansion (NN# arithmetic base syntax)",
				NodeType: childType}
		}
		return node.Text, nil

	case "raw_string":
		return stripRawString(node.Text), nil

	case "string":
		return walkString(node, innerCommands, varScope)

	case "concatenation":
		if braceExpansionRe.MatchString(node.Text) {
			return "", &ParseForSecurityResult{Kind: kindTooComplex,
				Reason: "Brace expansion", NodeType: "concatenation"}
		}
		result := ""
		for _, child := range node.Children {
			if child == nil {
				continue
			}
			part, errRes := walkArgument(child, innerCommands, varScope)
			if errRes != nil {
				return "", errRes
			}
			result += part
		}
		return result, nil

	case "arithmetic_expansion":
		if err := walkArithmetic(node); err != nil {
			return "", err
		}
		return node.Text, nil

	case "simple_expansion":
		// 拼接内的 `$VAR`（如 prefix$VAR）。与 walkCommand 裸参数同规则：
		// 须已跟踪或 SAFE_ENV_VARS。拼接内视同裸参数（整个拼接就是参数）。
		return resolveSimpleExpansion(node, varScope, false)

	// 注意：参数位置的 command_substitution（裸或拼接内）刻意不处理
	// ——输出是/成为位置参数的一部分，可能是路径或标志。`rm $(foo)` 或
	// `rm $(foo)bar` 会把真实路径藏在占位符后。仅 string 节点内的 $()
	//（walkString）被提取——输出内嵌于更长字符串而非本身是参数。

	default:
		return "", tooComplexPtr(node)
	}
}

// unescapeBackslashes 对齐 replace(/\\(.)/g, '$1')——反斜杠反转义。
func unescapeBackslashes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// walkString 提取双引号 string 节点的字面内容（对齐 walkString）。
// children 是 `"` 定界符、string_content 字面量与可能的展开节点。
//
// tree-sitter quirk：双引号内的字面换行不在 string_content 文本中。
// bash 保留它们。`"a\nb"`（真实换行）产两个 string_content（"a"、"b"），
// 换行不在任何一个里。`"\n#"` 产一个子节点（"#"），前导换行被吃。
// 简单拼接子节点会丢换行。
//
// 修复：跟踪子节点 startIndex，每个索引间隙插一个 `\n`。子节点间的
// 间隙就是被丢的换行——使 argv 值匹配 bash 实际所见。
func walkString(
	node *TsNode,
	innerCommands *[]SimpleCommand,
	varScope map[string]string,
) (string, *ParseForSecurityResult) {
	result := ""
	cursor := -1
	// 安全：跟踪串内是否含运行时未知占位符（$() 输出或未知值已跟踪
	// 变量）vs 字面内容。纯占位符串（"$(cmd)"、未知值的 "$VAR"）产出
	// 的 argv 元素就是占位符——下游路径校验把它解析为 cwd 内相对文件名，
	// 绕过检查。`cd "$(echo /etc)"` 会通过校验但运行时 cd 进 /etc。拒收
	// solo-placeholder；与字面内容混合的占位符（"prefix: $(cmd)"）安全
	// ——运行时值不可能等于裸路径。
	sawDynamicPlaceholder := false
	sawLiteralContent := false
	for _, child := range node.Children {
		if child == nil {
			continue
		}
		// 与前一子节点的索引间隙 = 被丢的换行。首个非定界符子节点前的
		// 间隙忽略（cursor === -1）。`"` 定界符跳过 gap 填充：闭合 `"` 前
		// 的间隙是 tree-sitter 纯空白串 quirk（空格/tab 非换行）——让
		// 下方检查按 too-complex 处理而非错填 `\n` 偏离 bash。
		if cursor != -1 && child.StartIndex > cursor && child.Type != `"` {
			for i := 0; i < child.StartIndex-cursor; i++ {
				result += "\n"
			}
			sawLiteralContent = true
		}
		cursor = child.EndIndex
		switch child.Type {
		case `"`:
			// 开引号后重置 cursor，捕获 `"` 与首个内容子节点的间隙。
			cursor = child.EndIndex
		case "string_content":
			// bash 双引号转义规则（非 walkArgument 的通用反转义）：
			// 内 `\` 只转义 $ ` " \ ——其他序列如 \n 保持字面。所以
			// `"fix \"bug\""` → `fix "bug"`，`"a\nb"` → `a\nb`（反斜杠
			// 保留）。tree-sitter 在 .text 保留原文转义，此处解析使 argv
			// 匹配 bash 实际传参。
			result += resolveDoubleQuoteEscapes(child.Text)
			sawLiteralContent = true
		case "$":
			// 闭合引号或非名字符前的裸 $ 在 bash 中是字面量。
			// tree-sitter 产为独立节点。
			result += "$"
			sawLiteralContent = true
		case "command_substitution":
			// 特例：`$(cat <<'EOF' ... EOF)` 安全。引号定界 heredoc 体是
			// 字面（无展开），cat 仅打印——替换结果是已知静态串。此模式
			// 是向 gh pr create --body 等传多行内容的惯用法。替换为占位符
			// argv 值——内容对权限检查无关紧要，只需它是静态的。
			heredocBody, dangerous := extractSafeCatHeredoc(child)
			if dangerous {
				return "", tooComplexPtr(child)
			}
			if heredocBody != nil {
				// 安全：体就是替换结果。此前丢弃 → `rm "$(cat <<'EOF'
				// /etc/passwd\nEOF)"` 产 argv ['rm',''] 而 bash 跑
				// `rm /etc/passwd`。validatePath('') 解析为 cwd → 放行。
				// 全部路径受限命令都经此绕过。现在追加体（尾 LF 去除
				// ——bash $() 剥尾换行）。
				//
				// 权衡：含内部换行的体是多行文本（markdown/脚本），不可能
				// 是合法路径——丢弃避免 NEWLINE_HASH_RE 对 `## Summary`
				// 的误报。单行体（如 /etc/passwd）必须进 argv 使下游路径
				// 校验看到真实目标。
				trimmed := strings.TrimRight(*heredocBody, "\n")
				if strings.Contains(trimmed, "\n") {
					sawLiteralContent = true
					break
				}
				result += trimmed
				sawLiteralContent = true
				break
			}
			// 通用 "..." 内 $()：递归内层命令。解析干净则成为额外子命令，
			// 权限系统须对其匹配规则。外层 argv 得原 $() 文本占位符
			//（运行时决定值）。`echo "SHA: $(git rev-parse HEAD)"` 提取
			// `echo "SHA: $(...)"` 与 `git rev-parse HEAD` 两条——两者都
			// 须匹配权限规则。约 27% 的 too-complex（top-5k ant 命令）。
			if err := collectCommandSubstitution(child, innerCommands, varScope); err != nil {
				return "", err
			}
			result += cmdsubPlaceholder
			sawDynamicPlaceholder = true
		case "simple_expansion":
			// "..." 内的 $VAR。已跟踪/安全变量解析；未跟踪拒收。
			v, errRes := resolveSimpleExpansion(child, varScope, true)
			if errRes != nil {
				return "", errRes
			}
			// VAR_PLACEHOLDER = 运行时未知（循环变量、read 变量、$() 输出、
			// SAFE_ENV_VARS、special vars）。其他字符串 = 已跟踪静态变量
			// 的实际字面值（VAR=/tmp → '/tmp'）。
			if v == varPlaceholder {
				sawDynamicPlaceholder = true
			} else {
				sawLiteralContent = true
			}
			result += v
		case "arithmetic_expansion":
			if err := walkArithmetic(child); err != nil {
				return "", err
			}
			result += child.Text
			// 已校验为字面数字——静态内容。
			sawLiteralContent = true
		default:
			// "..." 内的 expansion（${...}）
			return "", tooComplexPtr(child)
		}
	}
	// 安全：拒收 solo-placeholder 串。`"$(cmd)"` 或未知值的 `"$VAR"` 产出
	// 即占位符的 argv 元素——下游路径校验把它解析为 cwd 内相对文件名，
	// 绕过检查。仅允许占位符与字面内容一起内嵌（"prefix: $(cmd)"）。
	if sawDynamicPlaceholder && !sawLiteralContent {
		return "", tooComplexPtr(node)
	}
	// 安全：tree-sitter-bash quirk——仅含空白的双引号串（` "`、`" "`、
	// `"\t"`）不产 string_content 子节点；空白归到闭合 `"` 节点文本。
	// 循环只从 string_content/展开子节点累加内容，会返回 "" 而 bash 看到
	// " "。检测：未见过内容子节点（两标志均 false——字面与占位符都没加）
	// 但源 span 长于裸 `""`。真 `""` 的 text.length==2。`"$V"`（V=""）
	// 不触发——simple_expansion 子节点即使 v 为空也经 else 分支设
	// sawLiteralContent。
	if !sawLiteralContent && !sawDynamicPlaceholder && len(node.Text) > 2 {
		return "", tooComplexPtr(node)
	}
	return result, nil
}

// resolveDoubleQuoteEscapes bash 双引号内转义（对齐
// replace(/\\([$`"\\])/g, '$1')——内 \ 只转义 $ ` " \）。
func resolveDoubleQuoteEscapes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '$', '`', '"', '\\':
				b.WriteByte(s[i+1])
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// resolveSimpleExpansion 解析 simple_expansion（$VAR）节点（对齐
// resolveSimpleExpansion）。可解析返回 VAR_PLACEHOLDER 或实际值，
// 否则 too-complex。
//
// insideString：$VAR 在 string 节点内（"...$VAR..."）而非裸/拼接参数。
// SAFE_ENV_VARS 与未知值已跟踪变量仅在串内允许——裸参数时其运行时值
// 就是参数本身，静态未知。`cd $HOME/../x` 会把真实路径藏在占位符后；
// `echo "Home: $HOME"` 只是串内嵌文本。持静态字符串（VAR=literal）的
// 已跟踪变量两种位置都允许——值已知。
func resolveSimpleExpansion(
	node *TsNode,
	varScope map[string]string,
	insideString bool,
) (string, *ParseForSecurityResult) {
	varName := ""
	haveName := false
	isSpecial := false
	for _, c := range node.Children {
		if c == nil {
			continue
		}
		if c.Type == "variable_name" {
			varName = c.Text
			haveName = true
			break
		}
		if c.Type == "special_variable_name" {
			varName = c.Text
			isSpecial = true
			haveName = true
			break
		}
	}
	if !haveName {
		return "", tooComplexPtr(node)
	}
	// 已跟踪变量：查存储值。字面串（VAR=/tmp）直接返回真实值——下游
	// 路径校验/checkSemantics 基于真实路径操作。非字面值（含占位符——
	// 循环变量、$() 输出、read 变量、复合 `VAR="prefix$(cmd)"`）仅串内
	// 安全；裸参数会向校验隐藏运行时路径/标志。
	//
	// 安全：返回实际 trackedValue（非占位符）是关键修复。`VAR=/etc &&
	// rm $VAR` → argv ['rm','/etc'] → validatePath 正确拒收。此前返回
	// 占位符 → validatePath 视为 cwd 相对 → 放行 → 绕过。
	if trackedValue, ok := varScope[varName]; ok {
		if containsAnyPlaceholder(trackedValue) {
			// 非字面：裸 → 拒；串内 → VAR_PLACEHOLDER
			//（walkString 的 solo-placeholder 门拒收单独 `"$VAR"`）。
			if !insideString {
				return "", tooComplexPtr(node)
			}
			return varPlaceholder, nil
		}
		// 纯字面（'/tmp'、'foo'）——直接返回。
		//
		// 安全：裸参数 bash 对结果做词分割与 glob 展开。`VAR="-rf /" &&
		// rm $VAR` → bash 跑 `rm -rf /`（两参数）；`VAR="/etc/*" &&
		// cat $VAR` → 展开为全部 /etc 文件。含 IFS/glob 字符的值拒收
		//（"..." 内除外）。
		//
		// 安全：空值裸参数。bash 对 "" 词分割产生零字段——展开消失。
		// `V="" && $V eval x` → bash 跑 `eval x`（argv ["","eval","x"]
		// 名为 ""——所有内建检查漏过）。`V="" && ls $V /etc` → bash 跑
		// `ls /etc`，argv 有幻影 "" 移位。串内 `"$V"` → bash 产一个空串
		// 参数 → 我们的 "" 正确，放行。
		if !insideString {
			if trackedValue == "" {
				return "", tooComplexPtr(node)
			}
			if bareVarUnsafeRe.MatchString(trackedValue) {
				return "", tooComplexPtr(node)
			}
		}
		return trackedValue, nil
	}
	// SAFE_ENV_VARS + special vars（$?、$$、$@、$1 等）：值未知
	//（shell 控制）。仅串内安全，不作为路径敏感命令的裸参数。
	if insideString {
		if astSafeEnvVars[varName] {
			return varPlaceholder, nil
		}
		if isSpecial && (specialVarNames[varName] || isAllDigits(varName)) {
			return varPlaceholder, nil
		}
	}
	return "", tooComplexPtr(node)
}

// stripRawString 去单引号（对齐 stripRawString：text.slice(1,-1)）。
func stripRawString(text string) string {
	return text[1 : len(text)-1]
}
