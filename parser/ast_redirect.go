// ast_redirect.go——上游 eva-cli utils/bash/ast.ts 的重定向处理
//（1011-1230 行：walkRedirectedStatement/walkFileRedirect/
// walkHeredocRedirect/walkHerestringRedirect）的 Go 直译。

package parser

import "regexp"

// newlineHashRe argv 元素/env 值/重定向目标中的换行+`#`（对齐
// NEWLINE_HASH_RE）。下游 stripSafeWrappers 逐行重新分词 .text，把
// 换行后的 `#` 当注释，隐藏其后的参数。
var newlineHashRe = regexp.MustCompile(`\n[ \t]*#`)

// walkRedirectedStatement redirected_statement 包装一条命令（或管道）
// 与一个以上 file_redirect/heredoc_redirect 节点（对齐
// walkRedirectedStatement）。提取重定向、走内层命令、把重定向挂到
// 最后一条命令（输出被重定向的那条）。
func walkRedirectedStatement(
	node *TsNode,
	commands *[]SimpleCommand,
	varScope map[string]string,
) *ParseForSecurityResult {
	var redirects []Redirect
	var innerCommand *TsNode

	for _, child := range node.Children {
		if child == nil {
			continue
		}
		if child.Type == "file_redirect" {
			// 传递 commands 使重定向目标中的 $()（如 `> $(mktemp)`）提取
			// 内层命令供权限检查。
			r, errRes := walkFileRedirect(child, commands, varScope)
			if errRes != nil {
				return errRes
			}
			redirects = append(redirects, r)
		} else if child.Type == "heredoc_redirect" {
			if err := walkHeredocRedirect(child); err != nil {
				return err
			}
		} else if child.Type == "command" || child.Type == "pipeline" ||
			child.Type == "list" || child.Type == "negated_command" ||
			child.Type == "declaration_command" ||
			child.Type == "unset_command" {
			innerCommand = child
		} else {
			return tooComplexPtr(child)
		}
	}

	if innerCommand == nil {
		// `> file` 单独是合法 bash（截断文件）。表示为空 argv 命令使下游
		// 看到该写入。
		*commands = append(*commands, SimpleCommand{
			Argv: []string{}, EnvVars: []EnvVar{}, Redirects: redirects, Text: node.Text,
		})
		return nil
	}

	before := len(*commands)
	if err := collectCommands(innerCommand, commands, varScope); err != nil {
		return err
	}
	if len(*commands) > before && len(redirects) > 0 {
		last := &(*commands)[len(*commands)-1]
		last.Redirects = append(last.Redirects, redirects...)
	}
	return nil
}

// walkFileRedirect 从 file_redirect 节点提取算子与目标（对齐
// walkFileRedirect）。目标必须是静态 word 或 string。
func walkFileRedirect(
	node *TsNode,
	innerCommands *[]SimpleCommand,
	varScope map[string]string,
) (Redirect, *ParseForSecurityResult) {
	op := ""
	target := ""
	haveTarget := false
	var fd *int

	for _, child := range node.Children {
		if child == nil {
			continue
		}
		if child.Type == "file_descriptor" {
			fdVal := 0
			neg := false
			for i := 0; i < len(child.Text); i++ {
				if child.Text[i] == '-' {
					neg = true
					continue
				}
				fdVal = fdVal*10 + int(child.Text[i]-'0')
			}
			if neg {
				fdVal = -fdVal
			}
			fd = &fdVal
		} else if redirectOps[child.Type] != "" {
			op = redirectOps[child.Type]
		} else if child.Type == "word" || child.Type == "number" {
			// 安全：`number` 节点可经 `NN#<expansion>` 算术基底 grammar
			// quirk 携带展开子节点——与 walkArgument 的 number 分支同问题。
			// `> 10#$(cmd)` 运行时执行 cmd。纯 word/number 节点无子节点。
			if len(child.Children) > 0 {
				return Redirect{}, tooComplexPtr(child)
			}
			// 与 walkArgument（~608）对称：`echo foo > {a,b}` 在 bash 中是
			// 歧义重定向。tree-sitter 实际为花括号目标产 concatenation 节点
			//（下方 default 分支捕获），此处对 word 文本再查一道纵深防御。
			if braceExpansionRe.MatchString(child.Text) {
				return Redirect{}, tooComplexPtr(child)
			}
			// 反斜杠序列反转义——同 walkArgument。bash 引号剥离把
			// `\X` → X。否则 `cat < /proc/self/\environ` 存目标
			// `/proc/self/\environ` 逃避 PROC_ENVIRON_RE，而 bash 读
			// /proc/self/environ。
			target = unescapeBackslashes(child.Text)
			haveTarget = true
		} else if child.Type == "raw_string" {
			target = stripRawString(child.Text)
			haveTarget = true
		} else if child.Type == "string" {
			s, errRes := walkString(child, innerCommands, varScope)
			if errRes != nil {
				return Redirect{}, errRes
			}
			target = s
			haveTarget = true
		} else if child.Type == "concatenation" {
			// `echo > "foo"bar`——tree-sitter 产 string + word 子节点的
			// concatenation。walkArgument 已校验拼接（拒展开、查花括号
			// 语法）并返回拼接文本。
			s, errRes := walkArgument(child, innerCommands, varScope)
			if errRes != nil {
				return Redirect{}, errRes
			}
			target = s
			haveTarget = true
		} else {
			return Redirect{}, tooComplexPtr(child)
		}
	}

	if op == "" || !haveTarget {
		return Redirect{}, &ParseForSecurityResult{Kind: kindTooComplex,
			Reason:   "Unrecognized redirect shape",
			NodeType: node.Type}
	}
	return Redirect{Op: op, Target: target, Fd: fd}, nil
}

// walkHeredocRedirect heredoc 重定向。仅引号定界 heredoc（<<'EOF'）安全
// ——体是字面文本。非引号定界（<<EOF）体经历完整参数/命令/算术展开
// （对齐 walkHeredocRedirect）。
//
// 安全：tree-sitter-bash 有 grammar 缺口——非引号 heredoc 体内的反引号
// （`...`）不解析为 command_substitution 节点（body.children 为空，反引号
// 在 body.text 里）。但 bash 会执行。无法通过检查 body 子节点的展开节点
// 安全放宽引号定界要求——会漏反引号替换。继续拒收全部非引号 heredoc。
// 用户应使用 <<'EOF' 获得字面体，模型本就偏好此形态。
func walkHeredocRedirect(node *TsNode) *ParseForSecurityResult {
	startText := ""
	haveStart := false
	var body *TsNode

	for _, child := range node.Children {
		if child == nil {
			continue
		}
		if child.Type == "heredoc_start" {
			startText = child.Text
			haveStart = true
		} else if child.Type == "heredoc_body" {
			body = child
		} else if child.Type == "<<" || child.Type == "<<-" ||
			child.Type == "heredoc_end" || child.Type == "file_descriptor" {
			// 预期结构 token——安全跳过。file_descriptor 覆盖 fd 前缀
			// heredoc（`cat 3<<'EOF'`）——walkFileRedirect 同样视为良性
			// 结构 token。
		} else {
			// 安全：当管道/命令/file_redirect/&& 等跟在定界符同一行时
			//（如 `ls <<'EOF' | rm x`），tree-sitter 把它们作为
			// heredoc_redirect 的子节点。此前被静默跳过，向权限检查隐藏
			// 管道命令。与其他 walker 一致 fail closed。
			return tooComplexPtr(child)
		}
	}

	isQuoted := haveStart &&
		((stringsHasPrefix(startText, "'") && stringsHasSuffix(startText, "'")) ||
			(stringsHasPrefix(startText, `"`) && stringsHasSuffix(startText, `"`)) ||
			stringsHasPrefix(startText, `\`))

	if !isQuoted {
		return &ParseForSecurityResult{
			Kind:     kindTooComplex,
			Reason:   "Heredoc with unquoted delimiter undergoes shell expansion",
			NodeType: "heredoc_redirect",
		}
	}

	if body != nil {
		for _, child := range body.Children {
			if child == nil {
				continue
			}
			if child.Type != "heredoc_content" {
				return tooComplexPtr(child)
			}
		}
	}
	return nil
}

// walkHerestringRedirect here-string 重定向（`<<< content`）。content 成
// 为 stdin——非 argv 非路径。content 为无展开的字面 word/raw_string/
// string 时安全。content 含 $()/${}/$VAR 时拒——那些执行任意代码或
// 注入运行时值（对齐 walkHerestringRedirect）。
//
// 复用 walkArgument 校验内容：它已拒收 command_substitution、expansion，
// 以及（对 string）未跟踪/不安全的 simple_expansion。结果串被丢弃——
// 只关心它可静态解析。
//
// 注意：`VAR=$(cmd) && cat <<< "$VAR"` 原则上安全（内层命令单独提取，
// herestring 内容是 stdin）但目前被保守拒收——walkString 的
// solo-placeholder 门不了解 herestring 与 argv 上下文之别。
func walkHerestringRedirect(
	node *TsNode,
	innerCommands *[]SimpleCommand,
	varScope map[string]string,
) *ParseForSecurityResult {
	for _, child := range node.Children {
		if child == nil {
			continue
		}
		if child.Type == "<<<" {
			continue
		}
		// 内容节点：复用 walkArgument。成功返回字符串（丢弃——content 是
		// stdin，与权限无关）或失败 too-complex（发现展开、不可解析变量）。
		content, errRes := walkArgument(child, innerCommands, varScope)
		if errRes != nil {
			return errRes
		}
		// herestring 内容被丢弃（不进 argv/envVars/redirects）但仍保留在
		// node.text 原文里。在此扫描使 checkSemantics 的 NEWLINE_HASH
		// 不变量（bashPermissions.ts 依赖）仍成立。
		if newlineHashRe.MatchString(content) {
			return tooComplexPtr(child)
		}
	}
	return nil
}

// stringsHasSuffix 标准库语义（本地别名，直译文件内自洽）。
func stringsHasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
