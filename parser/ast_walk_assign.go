// ast_walk_assign.go——上游 eva-cli utils/bash/ast.ts 的赋值与算术处理
//（1675-1775 walkArithmetic/extractSafeCatHeredoc、1777-2031
// walkVariableAssignment/applyVarToScope、125-174 集合）的 Go 直译。

package parser

import (
	"regexp"
	"strings"
)

// astSafeEnvVars bash 自动设置、值由 shell/OS 控制的已知安全环境变量
// （对齐 ast.ts SAFE_ENV_VARS——注意与 bashPermissions.ts 的 SAFE_ENV_VARS
// 白名单是**不同集合**：此处是 $HOME/$PWD/$PATH 等运行时必然存在的
// 变量，$VAR 引用展开是确定性的，不引入注入风险）。刻意保持小：
// 只收恒被 bash/login 设置且值为路径/名称（非任意内容）的变量。
var astSafeEnvVars = map[string]bool{
	"HOME":     true, // 用户主目录
	"PWD":      true, // 当前目录（bash 维护）
	"OLDPWD":   true, // 前一目录
	"USER":     true, // 当前用户名
	"LOGNAME":  true, // 登录名
	"SHELL":    true, // 用户登录 shell
	"PATH":     true, // 可执行搜索路径
	"HOSTNAME": true,
	"UID":      true,
	"EUID":     true, // 有效用户 id
	"PPID":     true, // 父进程 id
	"RANDOM":   true, // bash 内建随机数
	"SECONDS":  true, // shell 启动秒数
	"LINENO":   true, // 当前行号
	"TMPDIR":   true, // 临时目录
	// 特殊 bash 变量——恒设置，值 shell 控制：
	"BASH_VERSION": true,
	"BASHPID":      true,
	"SHLVL":        true,
	"HISTFILE":     true,
	"IFS":          true, // 字段分隔符。注意：仅串内安全；裸 $IFS 是经典注入
	// 原语，resolveSimpleExpansion 的 insideString 门正确拦截
}

// specialVarNames 特殊 shell 变量（$?、$$、$!、$#、$0、$-）。tree-sitter
// 用 special_variable_name 表示（非 variable_name）。值 shell 控制：
// 退出码、PID、位置参数。安全解析（对齐 SPECIAL_VAR_NAMES）。
//
// 安全：'@' 与 '*' 不在集合内。"..." 内它们展开为位置参数——在新
// BashTool shell（我们恒这样 spawn）里为空。返回 VAR_PLACEHOLDER 会
// 撒谎：`git "push$*"` 得 argv ['git','push__TRACKED_VAR__'] 而 bash 传
// ['git','push']。deny 规则 Bash(git push:*) 在 .text（原始 $*）与重建
// argv（占位符）上都失配。移除后 resolveSimpleExpansion 对 $*/$@
// 落 tooComplex。`echo "args: $*"` 变 too-complex——可接受（BashTool
// 用法中罕见；"$@" 更罕见）。
var specialVarNames = map[string]bool{
	"?": true, // 上一命令退出码
	"$": true, // 当前 shell PID
	"!": true, // 最近后台 PID
	"#": true, // 位置参数个数
	"0": true, // 脚本名
	"-": true, // shell 选项标志
}

// arithLeafRe 算术展开内的安全叶子：整数字面（十进制、十六进制、
// 八进制、bash base#digits）与算子/括号 token。叶子位置的其他任何
// 内容（尤其非数字字面量的 variable_name）拒收（对齐 ARITH_LEAF_RE）。
var arithLeafRe = regexp.MustCompile(
	`^(?:[0-9]+|0[xX][0-9a-fA-F]+|[0-9]+#[0-9a-zA-Z]+|[-+*/%^&|~!<>=?:(),]+|<<|>>|\*\*|&&|\|\||[<>=!]=|\$\(\(|\)\))$`)

// walkArithmetic 递归校验 arithmetic_expansion 节点（对齐
// walkArithmetic）。只允许字面数字表达式——无变量无替换。安全返回
// nil，否则 too-complex。
//
// 变量拒收原因：bash 算术递归求值变量值：x='a[$(cmd)]' 时 $((x)) 执行
// cmd。见 https://www.vidarholen.net/contents/blog/?p=716（算术注入）。
//
// 安全时调用方把完整 `$((…))` span 作为字面字符串放进 argv。bash 运行
// 时展开为整数；静态字符串不会命中任何敏感路径/deny 模式。
func walkArithmetic(node *TsNode) *ParseForSecurityResult {
	for _, child := range node.Children {
		if child == nil {
			continue
		}
		if len(child.Children) == 0 {
			if !arithLeafRe.MatchString(child.Text) {
				return &ParseForSecurityResult{Kind: kindTooComplex,
					Reason:   "Arithmetic expansion references variable or non-literal: " + child.Text,
					NodeType: "arithmetic_expansion"}
			}
			continue
		}
		switch child.Type {
		case "binary_expression", "unary_expression",
			"ternary_expression", "parenthesized_expression":
			if err := walkArithmetic(child); err != nil {
				return err
			}
		default:
			return tooComplexPtr(child)
		}
	}
	return nil
}

// procEnvironRe 用 `.*` 而非 `[^/]*`——Linux 解析 procfs 中的 `..`，
// `/proc/self/../self/environ` 可用，必须捕获（对齐 PROC_ENVIRON_RE）。
var procEnvironRe = regexp.MustCompile(`/proc/.*/environ`)

// regexpJqSystem 对齐 /\bsystem\s*\(/。
var regexpJqSystem = regexp.MustCompile(`\bsystem\s*\(`)

// extractSafeCatHeredoc 检查 command_substitution 节点是否恰为
// `$(cat <<'DELIM'...DELIM)` 并返回 heredoc 体（对齐 extractSafeCatHeredoc，
// 1721-1775 行）。三态：body 非 nil = 安全形态（体可能为空串）；
// dangerous = true = 体含 /proc/*/environ 或 jq system()（拒收）；
// 否则 = 非此形态（调用方落回通用替换处理）。
//
// tree-sitter 结构：
//
//	command_substitution
//	  $(
//	  redirected_statement
//	    command → command_name → word "cat"    （恰一个子节点）
//	    heredoc_redirect
//	      <<
//	      heredoc_start 'DELIM'                （引号）
//	      heredoc_body                         （纯 heredoc_content）
//	      heredoc_end
//	  )
func extractSafeCatHeredoc(subNode *TsNode) (body *string, dangerous bool) {
	// 期望恰为：$( + 一个 redirected_statement + )
	var stmt *TsNode
	for _, child := range subNode.Children {
		if child == nil {
			continue
		}
		if child.Type == "$(" || child.Type == ")" {
			continue
		}
		if child.Type == "redirected_statement" && stmt == nil {
			stmt = child
		} else {
			return nil, false
		}
	}
	if stmt == nil {
		return nil, false
	}

	// redirected_statement 必须是：command(cat) + heredoc_redirect（引号）
	sawCat := false
	for _, child := range stmt.Children {
		if child == nil {
			continue
		}
		if child.Type == "command" {
			// 必须是裸 `cat`——无参数无环境变量
			var cmdChildren []*TsNode
			for _, c := range child.Children {
				if c != nil {
					cmdChildren = append(cmdChildren, c)
				}
			}
			if len(cmdChildren) != 1 {
				return nil, false
			}
			if cmdChildren[0].Type != "command_name" || cmdChildren[0].Text != "cat" {
				return nil, false
			}
			sawCat = true
		} else if child.Type == "heredoc_redirect" {
			// 复用现有校验器：引号定界、体为纯文本。
			// walkHeredocRedirect 成功返回 nil，拒收返回非 nil。
			if walkHeredocRedirect(child) != nil {
				return nil, false
			}
			for _, hc := range child.Children {
				if hc != nil && hc.Type == "heredoc_body" {
					s := hc.Text
					body = &s
				}
			}
		} else {
			return nil, false
		}
	}

	if !sawCat || body == nil {
		return nil, false
	}
	// 安全：heredoc 体经替换成为外层命令的 argv 值，`/proc/self/environ`
	// 体在语义上就是 `cat /proc/self/environ`。checkSemantics 永远看不到
	// 体（walkString 调用点丢弃避免换行+# 误报）。此处返回 nil 会落入
	// walkString 的 collectCommandSubstitution——经 walkHeredocRedirect
	//（不检查体文本）提取内层 cat——实际绕过本检查。返回专用哨兵让调用
	// 方直接拒收而非穿透。
	if procEnvironRe.MatchString(*body) {
		return nil, true
	}
	// jq system() 同理：checkSemantics 检查 argv 看不到 heredoc 体。
	// 无条件检查（不知道外层命令）。
	if regexpJqSystem.MatchString(*body) {
		return nil, true
	}
	return body, false
}

// varAssign 变量赋值解析产物（对齐 walkVariableAssignment 返回对象）。
type varAssign struct {
	name     string
	value    string
	isAppend bool
}

// walkVariableAssignment 走 variable_assignment 节点（对齐
// walkVariableAssignment，1777-1922 行）。
func walkVariableAssignment(
	node *TsNode,
	innerCommands *[]SimpleCommand,
	varScope map[string]string,
) (varAssign, *ParseForSecurityResult) {
	name := ""
	haveName := false
	value := ""
	isAppend := false

	for _, child := range node.Children {
		if child == nil {
			continue
		}
		if child.Type == "variable_name" {
			name = child.Text
			haveName = true
		} else if child.Type == "=" || child.Type == "+=" {
			// `PATH+=":/new"`——tree-sitter 把 `+=` 产为独立算子节点。
			// 无此分支会落入下方 walkArgument → 未知类型 `+=` →
			// too-complex。
			isAppend = child.Type == "+="
			continue
		} else if child.Type == "command_substitution" {
			// $() 作为变量值。输出成为存储的字符串——不是位置参数（无
			// 路径/标志顾虑）。`VAR=$(date)` 运行 date 存输出。`VAR=
			// $(rm -rf /)` 会跑 rm——内层命令照常过权限规则，rm 须匹配
			// 规则。变量只存 rm 打印的内容。
			if err := collectCommandSubstitution(child, innerCommands, varScope); err != nil {
				return varAssign{}, err
			}
			value = cmdsubPlaceholder
		} else if child.Type == "simple_expansion" {
			// `VAR=$OTHER`——赋值 RHS 在 bash 中不做词分割/glob 展开
			//（不同于命令参数）。`A="a b"; B=$A` 把 B 设为字面 "a b"。
			// 按 insideString=true 解析使 BARE_VAR_UNSAFE_RE 不过拒。
			// 结果值可含空格/glob——B 后续被用作裸参数时才正确拒收。
			v, errRes := resolveSimpleExpansion(child, varScope, true)
			if errRes != nil {
				return varAssign{}, errRes
			}
			// v 为 VAR_PLACEHOLDER（OTHER 未知值）时照存——调用方经
			// containsAnyPlaceholder 按未知处理。
			value = v
		} else {
			v, errRes := walkArgument(child, innerCommands, varScope)
			if errRes != nil {
				return varAssign{}, errRes
			}
			value = v
		}
	}

	if !haveName {
		return varAssign{}, &ParseForSecurityResult{Kind: kindTooComplex,
			Reason:   "Variable assignment without name",
			NodeType: "variable_assignment"}
	}
	// 安全：tree-sitter-bash 接受非法变量名（如 `1VAR=value`）为
	// variable_assignment。bash 只认 [A-Za-z_][A-Za-z0-9_]*——其他按
	// 命令从 PATH 运行。`1VAR=value` → bash 尝试执行 `1VAR=value`。
	// 不得当惰性赋值处理。
	if !isIdentifier(name) {
		return varAssign{}, &ParseForSecurityResult{Kind: kindTooComplex,
			Reason:   "Invalid variable name (bash treats as command): " + name,
			NodeType: "variable_assignment"}
	}
	// 安全：设 IFS 改变后续非引号 $VAR 展开的词分割行为。`IFS=: &&
	// VAR=a:b && rm $VAR` → bash 按 `:` 分割 → `rm a b`。BARE_VAR_UNSAFE_RE
	// 只查默认 IFS 字符（空格/tab/NL）——无法建模自定义 IFS。拒。
	if name == "IFS" {
		return varAssign{}, &ParseForSecurityResult{Kind: kindTooComplex,
			Reason:   "IFS assignment changes word-splitting — cannot model statically",
			NodeType: "variable_assignment"}
	}
	// 安全：PS4 经 promptvars（默认开）在 `set -x` 后每条被追踪命令展开。
	// 含 $(cmd) 或 `cmd` 的 raw_string 值在追踪期执行：`PS4='$(id)' &&
	// set -x && :` 跑 id，但我们的 argv 只有 [["set","-x"],[":"]]——载荷
	// 对权限检查不可见。PS0-3 与 PROMPT_COMMAND 在非交互 shell（BashTool）
	// 不展开。
	//
	// 白名单而非黑名单。五轮绕过补丁证明值相关黑名单结构性脆弱：
	//  - `+=` 有效值计算在多个 scope 模型缺口上与 bash 分歧：`||` 重置、
	//    env 前缀链（PS4='' && PS4='$' PS4+='(id)' cmd 读到旧父值）、子 shell
	//  - bash 的 decode_prompt_string 在 promptvars 之前运行，\044(id)
	//    （$ 的八进制）追踪期变 $(id)——任何字面字符检查都必须精确建模
	//    prompt 转义解码
	//  - 赋值路径存在于 walkVariableAssignment 之外（for_statement 直接
	//    设循环变量，见该处理器 PS4 检查）
	//
	// 策略：(1) 拒 +=——不依赖 scope 跟踪；用户可合并为单条 PS4=...
	// (2) 拒占位符——运行时不可知 (3) 剩余值白名单：${identifier} 引用
	//（只读值，安全）+ [A-Za-z0-9 _+:.\/=[\]-]。无裸 $（封分裂原语）、
	// 无 `\`（封八进制 \044/\140）、无反引号、无括号。覆盖所有已知编码
	// 向量与未来向量——白名单之外一律失败。合法
	// `PS4='+${BASH_SOURCE}:${LINENO}: '` 仍通过。
	if name == "PS4" {
		if isAppend {
			return varAssign{}, &ParseForSecurityResult{Kind: kindTooComplex,
				Reason:   "PS4 += cannot be statically verified — combine into a single PS4= assignment",
				NodeType: "variable_assignment"}
		}
		if containsAnyPlaceholder(value) {
			return varAssign{}, &ParseForSecurityResult{Kind: kindTooComplex,
				Reason:   "PS4 value derived from cmdsub/variable — runtime unknowable",
				NodeType: "variable_assignment"}
		}
		if !ps4ValueAllowlist(value) {
			return varAssign{}, &ParseForSecurityResult{Kind: kindTooComplex,
				Reason:   "PS4 value outside safe charset — only ${VAR} refs and [A-Za-z0-9 _+:.=/[]-] allowed",
				NodeType: "variable_assignment"}
		}
	}
	// 安全：赋值 RHS 的波浪号展开。`VAR=~/x`（非引号）→ bash 赋值期
	// 展开 `~` → VAR='/home/user/x'，我们看到字面 `~/x`。之后 `cd $VAR`
	// → argv ['cd','~/x']，bash 跑 `cd /home/user/x`。波浪号在赋值值的
	// `=` 与 `:` 之后同样展开（PATH=~/bin:~/sbin）。无法建模——值含 `~`
	// 且非已引号字面（bash 不展开）即拒。保守：值含任何 `~` 拒。
	if strings.Contains(value, "~") {
		return varAssign{}, &ParseForSecurityResult{Kind: kindTooComplex,
			Reason:   "Tilde in assignment value — bash may expand at assignment time",
			NodeType: "variable_assignment"}
	}
	return varAssign{name: name, value: value, isAppend: isAppend}, nil
}

// ps4VarRefRe 对齐 /\$\{[A-Za-z_][A-Za-z0-9_]*\}/g。
var ps4VarRefRe = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)

// ps4ValueAllowlist PS4 值白名单（对齐 PS4 检查的字符白名单步骤）：
// 剥 ${VAR} 引用后仅允许 [A-Za-z0-9 _+:.\/=[\]-]。
func ps4ValueAllowlist(value string) bool {
	stripped := ps4VarRefRe.ReplaceAllString(value, "")
	for i := 0; i < len(stripped); i++ {
		c := stripped[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == ' ' || c == '_' || c == '+' || c == ':' || c == '.' ||
			c == '/' || c == '=' || c == '[' || c == ']' || c == '-':
		default:
			return false
		}
	}
	return true
}

// applyVarToScope 变量赋值入 scope，处理 `+=` 追加语义（对齐
// applyVarToScope）。安全：任一侧（既有值或追加值）含占位符则结果非
// 字面——存 VAR_PLACEHOLDER 使后续 $VAR 正确按裸参数拒收。
// `VAR=/etc && VAR+=$(cmd)` 不得让 VAR 显得静态。
func applyVarToScope(varScope map[string]string, ev varAssign) {
	existing := varScope[ev.name]
	combined := ev.value
	if ev.isAppend {
		combined = existing + ev.value
	}
	if containsAnyPlaceholder(combined) {
		varScope[ev.name] = varPlaceholder
	} else {
		varScope[ev.name] = combined
	}
}
