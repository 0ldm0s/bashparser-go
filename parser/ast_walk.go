// ast_walk.go——上游 eva-cli utils/bash/ast.ts collectCommands 主遍历
//（462-577、882-955 行）的 Go 直译：节点白名单架构，未显式处理的
// 节点类型一律 too-complex（安全兜底）。

package parser

import "strings"

// walkProgram 从根节点提取扁平命令列表（对齐 walkProgram）。
// ERROR 节点检查并入 collectCommands——任何未处理类型（含 ERROR）在
// default 分支落入 tooComplex，避免单独的全树错误遍历。
func walkProgram(root *TsNode) ParseForSecurityResult {
	commands := []SimpleCommand{}
	// varScope 跟踪同一命令内先期赋值的变量。simple_expansion（$VAR）
	// 引用已跟踪变量时以占位符替代而非返回 too-complex。支持
	// `NOW=$(date) && jq --arg now "$NOW" ...`——$NOW 是已知 $(date) 输出
	//（内层命令已提取）。
	varScope := map[string]string{}
	if err := collectCommands(root, &commands, varScope); err != nil {
		return *err
	}
	return simpleResult(commands)
}

// collectCommands 从结构包装节点递归收集叶子 command 节点（对齐
// collectCommands）。遇不允许的节点类型返回错误结果，成功返回 nil。
func collectCommands(
	node *TsNode,
	commands *[]SimpleCommand,
	varScope map[string]string,
) *ParseForSecurityResult {
	if node.Type == "command" {
		// 传 commands 作为 innerCommands 累加器——walkCommand 提取的
		// $() 内层命令与外层命令并列追加。
		result := walkCommand(node, nil, commands, varScope)
		if result.Kind != kindSimple {
			return &result
		}
		*commands = append(*commands, result.Commands...)
		return nil
	}

	if node.Type == "redirected_statement" {
		return walkRedirectedStatement(node, commands, varScope)
	}

	if node.Type == "comment" {
		return nil
	}

	if structuralTypes[node.Type] {
		// 安全：`||`、`|`、`|&`、`&` 不得线性携带 varScope。bash 中：
		//   `||` 右侧条件执行 → 变量可能未设
		//   `|`/`|&` 各级在子 shell 运行 → 变量之后永不可见
		//   `&` 左侧在后台子 shell 运行 → 同上
		// 标志省略攻击：`true || FLAG=--dry-run && cmd $FLAG`——bash 跳过
		// `||` 右侧（FLAG 未设 → $FLAG 空），cmd 不带 --dry-run 运行。
		// 线性作用域下 argv 是 ['cmd','--dry-run'] → 看似安全 → 绕过。
		//
		// 修复：入口快照。这些分隔符之后重置为快照——子句间设置的变量
		// 不泄漏。`&&`/`;` 链之间共享状态（常见 `VAR=x && cmd $VAR`）。
		//
		// 注意：快照后 scope 与 varScope 在首个 `||`/`|`/`&` 后分歧。
		// 调用方 varScope 仅被 `&&`/`;` 前缀改变——保守（A && B | C && D
		// 中 A+B 泄漏给调用方而非 C+D）但安全。
		//
		// 效率：仅在命中 `||`/`|`/`|&`/`&` 时才需要快照。主导场景
		//（ls、git status——无此类分隔符）经廉价预扫描跳过 Map 分配。
		// pipeline 无需预扫描（node.type 已表明各段是子 shell）——入口
		// 拷贝一次即可。
		isPipeline := node.Type == "pipeline"
		needsSnapshot := false
		if !isPipeline {
			for _, c := range node.Children {
				if c != nil && (c.Type == "||" || c.Type == "&") {
					needsSnapshot = true
					break
				}
			}
		}
		var snapshot map[string]string
		if needsSnapshot {
			snapshot = copyScope(varScope)
		}
		// pipeline：所有段在子 shell 运行——以拷贝开始，不污染调用方
		// scope。list/program：`&&`/`;` 链改变调用方 scope（顺序执行），
		// 仅在 `||`/`&` 处分叉。
		scope := varScope
		if isPipeline {
			scope = copyScope(varScope)
		}
		for _, child := range node.Children {
			if child == nil {
				continue
			}
			if separatorTypes[child.Type] {
				if child.Type == "||" || child.Type == "|" ||
					child.Type == "|&" || child.Type == "&" {
					// pipeline：varScope 未被触碰（从拷贝开始）。
					// list/program：快照非 nil（预扫描已设）。
					// `|`/`|&` 仅出现在 pipeline 下；`||`/`&` 在 list 下。
					scope = copyScope(snapshotOr(varScope, snapshot))
				}
				continue
			}
			if err := collectCommands(child, commands, scope); err != nil {
				return err
			}
		}
		return nil
	}

	if node.Type == "negated_command" {
		// `! cmd` 仅反转退出码——不执行代码不影响 argv。递归内层命令。
		// CI 常见：`! grep err`、`! test -f lock`、`! git diff --quiet`。
		for _, child := range node.Children {
			if child == nil {
				continue
			}
			if child.Type == "!" {
				continue
			}
			return collectCommands(child, commands, varScope)
		}
		return nil
	}

	if node.Type == "declaration_command" {
		return walkDeclarationCommand(node, commands, varScope)
	}

	if node.Type == "variable_assignment" {
		// 语句级裸 `VAR=value`（非命令环境前缀）。设 shell 变量——无代码
		// 执行无文件 I/O。值经 walkVariableAssignment → walkArgument 校验，
		// `VAR=$(evil)` 仍递归提取/按内层命令拒收。不进 commands——裸赋值
		// 无需权限规则（惰性）。常见：`VAR=x && cmd`。约 35% 的 too-complex。
		ev, errRes := walkVariableAssignment(node, commands, varScope)
		if errRes != nil {
			return errRes
		}
		applyVarToScope(varScope, ev)
		return nil
	}

	if node.Type == "for_statement" {
		return walkForStatement(node, commands, varScope)
	}

	if node.Type == "if_statement" || node.Type == "while_statement" {
		return walkIfWhileStatement(node, commands, varScope)
	}

	if node.Type == "subshell" {
		// `(cmd1; cmd2)`——子 shell 中运行命令。内层命令会被执行，提取供
		// 权限检查。子 shell 作用域隔离：scope 拷贝（外层变量可见，内层
		// 修改丢弃）。
		innerScope := copyScope(varScope)
		for _, child := range node.Children {
			if child == nil {
				continue
			}
			if child.Type == "(" || child.Type == ")" {
				continue
			}
			if err := collectCommands(child, commands, innerScope); err != nil {
				return err
			}
		}
		return nil
	}

	if node.Type == "test_command" {
		// `[[ EXPR ]]` 或 `[ EXPR ]`——条件测试。基于文件测试/字符串比较
		// 求值真伪。无代码执行（内部无 command_substitution——那是子节点，
		// 经 walkArgument 递归拒收）。argv[0]='[[' 合成命令供规则匹配。
		// 走参数校验（操作数内无 cmdsub/展开）。
		argv := []string{"[["}
		for _, child := range node.Children {
			if child == nil {
				continue
			}
			if child.Type == "[[" || child.Type == "]]" ||
				child.Type == "[" || child.Type == "]" {
				continue
			}
			// 递归测试表达式结构：unary/binary/parenthesized/negated
			// expression。叶子是 test_operator（-f, -d, ==）与操作数词。
			if err := walkTestExpr(child, &argv, commands, varScope); err != nil {
				return err
			}
		}
		*commands = append(*commands, SimpleCommand{
			Argv: argv, EnvVars: []EnvVar{}, Redirects: []Redirect{}, Text: node.Text,
		})
		return nil
	}

	if node.Type == "unset_command" {
		return walkUnsetCommand(node, commands, varScope)
	}

	return tooComplexPtr(node)
}

// walkUnsetCommand `unset FOO BAR`、`unset -f func`（对齐 920-952 行）。
// 安全：仅从当前 shell 移除变量/函数——无代码执行无文件 I/O。
// tree-sitter 有专用节点类型，此前落 too-complex。
func walkUnsetCommand(
	node *TsNode,
	commands *[]SimpleCommand,
	varScope map[string]string,
) *ParseForSecurityResult {
	argv := []string{}
	for _, child := range node.Children {
		if child == nil {
			continue
		}
		switch child.Type {
		case "unset":
			argv = append(argv, child.Text)
		case "variable_name":
			argv = append(argv, child.Text)
			// 安全：unset 从 bash 作用域移除变量。从 varScope 删除，后续
			// $VAR 引用正确拒收。`VAR=safe && unset VAR && rm $VAR` 不得
			// 解析 $VAR。
			delete(varScope, child.Text)
		case "word":
			arg, errRes := walkArgument(child, commands, varScope)
			if errRes != nil {
				return errRes
			}
			argv = append(argv, arg)
		default:
			return tooComplexPtr(child)
		}
	}
	*commands = append(*commands, SimpleCommand{
		Argv: argv, EnvVars: []EnvVar{}, Redirects: []Redirect{}, Text: node.Text,
	})
	return nil
}

// ── 作用域/小工具 ──

// copyScope 作用域拷贝（对齐 new Map(varScope)）。
func copyScope(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// snapshotOr 返回快照（非 nil）或回退值。
func snapshotOr(fallback, snapshot map[string]string) map[string]string {
	if snapshot != nil {
		return snapshot
	}
	return fallback
}

// containsAnyPlaceholder 值含任一占位符（精确或内嵌）——非纯字面量
// （对齐 containsAnyPlaceholder：子串检查捕获复合形态
// `VAR="prefix$(cmd)"` → "prefix__CMDSUB_OUTPUT__"，以及用户字面量撞车
// `VAR=__TRACKED_VAR__ && rm $VAR`——保守按非字面量处理）。
func containsAnyPlaceholder(value string) bool {
	return strings.Contains(value, cmdsubPlaceholder) ||
		strings.Contains(value, varPlaceholder)
}

// isIdentifier bash 标识符：[A-Za-z_][A-Za-z0-9_]*
func isIdentifier(s string) bool {
	if len(s) == 0 {
		return false
	}
	c := s[0]
	if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_') {
		return false
	}
	for i := 1; i < len(s); i++ {
		c = s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' ||
			c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// regexpFlagNiaA 对齐 /^-[a-zA-Z]*[niaA]/——declare 族改变赋值语义的
// 标志。- 后字母序列任意位置出现 n/i/a/A 即命中；非字母终止且其前无
// n/i/a/A 则不匹配（正则为无 $ 锚的前缀匹配）。
func regexpFlagNiaA(arg string) bool {
	if len(arg) == 0 || arg[0] != '-' {
		return false
	}
	for i := 1; i < len(arg); i++ {
		c := arg[i]
		switch {
		case c == 'n' || c == 'i' || c == 'a' || c == 'A':
			return true
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			continue
		default:
			return false
		}
	}
	return false
}

// containsSubscript 对齐 /^[^=]*\[/——首个 '=' 之前含 '['。
func containsSubscript(arg string) bool {
	for i := 0; i < len(arg); i++ {
		if arg[i] == '[' {
			return true
		}
		if arg[i] == '=' {
			return false
		}
	}
	return false
}

// stringsHasPrefix 标准库语义（本地别名，直译文件内自洽）。
func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
