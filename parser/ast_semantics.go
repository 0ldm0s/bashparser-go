// ast_semantics.go——上游 eva-cli utils/bash/ast.ts 的 post-argv 语义
// 检查（2043-2680 行）的 Go 直译。
//
// 上方（ast_walk*）回答"能否分词"；本档回答"产出的 argv 是否存在与
// 解析无关的危险"。这些检查基于 argv[0] 或 argv 内容，旧 bashSecurity.ts
// 校验器也做过但与解析差异无关。它们放在这里（不在 bashSecurity）因为
// 操作 SimpleCommand、须对每条提取的命令运行。
// 检查用集合/正则常量见 ast_semantics_data.go。

package parser

import (
	"strings"
)

// SemanticCheckResult 语义检查结果（对齐 SemanticCheckResult）。
type SemanticCheckResult struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// CheckSemantics post-argv 语义检查（对齐 checkSemantics）。在
// parseForSecurity 返回 simple 后运行，捕获分词正常但按名称或参数
// 内容危险的命令。返回首个失败或 {ok: true}。
func CheckSemantics(commands []SimpleCommand) SemanticCheckResult {
	for _, cmd := range commands {
		// 剥安全包装命令（nohup、time、timeout N、nice -n N）使
		// `nohup eval "..."`、`timeout 5 jq 'system(...)'` 对被包装命令
		// 检查而非包装器。内联以避免与 bashPermissions.ts 循环 import。
		a := cmd.Argv
		for {
			if len(a) > 0 && (a[0] == "time" || a[0] == "nohup") {
				a = a[1:]
			} else if len(a) > 0 && a[0] == "timeout" {
				// `timeout 5`、`timeout 5s`、`timeout 5.5` 加可选 GNU 长标志。
				// 长：--foreground、--kill-after=N、--signal=SIG、
				// --preserve-status。短：-k DUR、-s SIG、-v（含融合 -k5、-sTERM）。
				// 安全（SAST 2026-03）：此前循环只跳 `--long` 标志，
				// `timeout -k 5 10 eval ...` 以 name='timeout' 退出，被包装的
				// eval 从未被检查。现在处理已知短标志并对任何未识别标志
				// fail closed——未知标志意味着无法定位被包装命令，不得静默
				// 落到 name='timeout'。
				i := 1
				for i < len(a) {
					arg := a[i]
					if arg == "--foreground" || arg == "--preserve-status" ||
						arg == "--verbose" {
						i++ // 已知无值长标志
					} else if regexpTimeoutFusedLong.MatchString(arg) {
						i++ // --kill-after=5、--signal=TERM（= 融合值）
					} else if (arg == "--kill-after" || arg == "--signal") &&
						i+1 < len(a) && timeoutFlagValueRe.MatchString(a[i+1]) {
						i += 2 // --kill-after 5、--signal TERM（空格分隔值）
					} else if stringsHasPrefix(arg, "--") {
						// 未知长标志，或 --kill-after/--signal 带非白名单值
						//（如 $() 替换占位符）。fail closed。
						return SemanticCheckResult{OK: false,
							Reason: "timeout with " + arg + " flag cannot be statically analyzed"}
					} else if arg == "-v" {
						i++ // --verbose，无参数
					} else if (arg == "-k" || arg == "-s") &&
						i+1 < len(a) && timeoutFlagValueRe.MatchString(a[i+1]) {
						i += 2 // -k DURATION / -s SIGNAL——分离值
					} else if regexpTimeoutFusedShort.MatchString(arg) {
						i++ // 融合：-k5、-sTERM
					} else if stringsHasPrefix(arg, "-") {
						// 未知标志或 -k/-s 带非白名单值——无法定位被包装
						// 命令。拒收，不落到 name='timeout'。
						return SemanticCheckResult{OK: false,
							Reason: "timeout with " + arg + " flag cannot be statically analyzed"}
					} else {
						break // 非标志——应为时长
					}
				}
				if i < len(a) && regexpDuration.MatchString(a[i]) {
					a = a[i+1:]
				} else if i < len(a) {
					// 安全（PR #21503 第三轮）：a[i] 存在但不匹配时长正则。
					// GNU timeout 经 xstrtod()（libc strtod）解析，接受 `.5`、
					// `+5`、`5e-1`、`inf`、`infinity`、十六进制浮点——均不匹配
					// `/^\d+(\.\d+)?[smhd]?$/`。实证 `timeout .5 echo ok`
					// 可用。此前此分支 break（fail-OPEN），
					// `timeout .5 eval "id"` 配 `Bash(timeout:*)` 时 name='timeout'、
					// eval 从未被检查。现在 fail CLOSED——与上方未知标志
					// 处理一致。
					return SemanticCheckResult{OK: false,
						Reason: "timeout duration '" + a[i] + "' cannot be statically analyzed"}
				} else {
					break // 无更多参数——裸 `timeout`，惰性
				}
			} else if len(a) > 0 && a[0] == "nice" {
				// `nice cmd`、`nice -n N cmd`、`nice -N cmd`（旧式）。均以更低
				// 优先级运行 cmd。argv[0] 检查必须看到被包装 cmd。
				if len(a) > 2 && a[1] == "-n" && regexpInt.MatchString(a[2]) {
					a = a[3:]
				} else if len(a) > 1 && regexpNegInt.MatchString(a[1]) {
					a = a[2:] // `nice -10 cmd`
				} else if len(a) > 1 && regexpDollarBacktick.MatchString(a[1]) {
					// 安全：walkArgument 对 arithmetic_expansion 返回 node.text，
					// `nice $((0-5)) jq ...` 的 a[1]='$((0-5))'。bash 展开为
					// '-5'（旧式 nice 语法）exec jq；此处 slice(1) 会把 name
					// 设为 '$((0-5))'，完全跳过 jq 的 system() 检查。fail
					// closed——镜像上方 timeout 时长的 fail-closed。
					return SemanticCheckResult{OK: false,
						Reason: "nice argument '" + a[1] + "' contains expansion — cannot statically determine wrapped command"}
				} else {
					a = a[1:] // 裸 `nice cmd`
				}
			} else if len(a) > 0 && a[0] == "env" {
				// `env [VAR=val...] [-i] [-0] [-v] [-u NAME...] cmd args` 运行
				// cmd。argv[0] 检查必须看到 cmd 而非 env。仅跳过已知安全形态。
				// 安全：-S 把字符串拆成 argv（迷你 shell）——必须拒。
				// -C/-P 改变 cwd/PATH——被包装 cmd 在别处运行，拒。
				// 其他任何标志 → 拒（fail-closed，非 fail-open 到 name='env'）。
				i := 1
				for i < len(a) {
					arg := a[i]
					if strings.Contains(arg, "=") && !stringsHasPrefix(arg, "-") {
						i++ // VAR=val 赋值
					} else if arg == "-i" || arg == "-0" || arg == "-v" {
						i++ // 无参标志
					} else if arg == "-u" && i+1 < len(a) {
						i += 2 // -u NAME 取消设置；取一个参数
					} else if stringsHasPrefix(arg, "-") {
						// -S（argv 拆分）、-C（换目录）、-P（换 PATH）、
						// --任意、或未知标志。无法建模——整条命令拒。
						return SemanticCheckResult{OK: false,
							Reason: "env with " + arg + " flag cannot be statically analyzed"}
					} else {
						break // 被包装命令
					}
				}
				if i < len(a) {
					a = a[i:]
				} else {
					break // 裸 `env`（无被包装 cmd）——惰性，name='env'
				}
			} else if len(a) > 0 && a[0] == "stdbuf" {
				// `stdbuf -o0 cmd`（融合）、`stdbuf -o 0 cmd`（空格分隔）、
				// 多标志（`stdbuf -o0 -eL cmd`）、长形态（`--output=0`）。
				// 安全：此前只剥一个标志并对未识别形态 slice(2)，
				// `stdbuf --output 0 eval` → ['0','eval',...] → name='0'
				// 隐藏 eval。现在迭代全部已知标志形态，未知标志 fail closed。
				i := 1
				for i < len(a) {
					arg := a[i]
					if stdbufShortSepRe.MatchString(arg) && i+1 < len(a) {
						i += 2 // -o MODE（空格分隔）
					} else if stdbufShortFusedRe.MatchString(arg) {
						i++ // -o0（融合）
					} else if stdbufLongRe.MatchString(arg) {
						i++ // --output=MODE（融合长）
					} else if stringsHasPrefix(arg, "-") {
						// --output MODE（空格分隔长形态）或未知标志。GNU
						// stdbuf 长选项用 `=` 语法，但 getopt_long 也接受
						// 空格分隔——无法安全枚举，拒。
						return SemanticCheckResult{OK: false,
							Reason: "stdbuf with " + arg + " flag cannot be statically analyzed"}
					} else {
						break // 被包装命令
					}
				}
				if i > 1 && i < len(a) {
					a = a[i:]
				} else {
					break // `stdbuf` 无标志或无被包装 cmd——惰性
				}
			} else {
				break
			}
		}
		if len(a) == 0 {
			continue
		}
		name := a[0]

		// 安全：空命令名。带引号空串（`"" cmd`）无害——bash 尝试 exec ""
		// 失败报 "command not found"。但命令位置的非引号空展开
		//（`V="" && $V cmd`）是绕过：bash 丢弃空字段以 `cmd` 为 argv[0]
		// 运行，而我们的 name="" 跳过下方全部内建检查。
		// resolveSimpleExpansion 拒收 $V 情形；此处捕获任何其他到达空
		// argv[0] 的路径（空拼接、walkString 空白 quirk、未来 bug）。
		if name == "" {
			return SemanticCheckResult{OK: false,
				Reason: "Empty command name — argv[0] may not reflect what bash runs"}
		}

		// 纵深防御：变量追踪修复后 argv[0] 不应再是占位符（静态变量返回
		// 真值，未知变量拒收）。但若上游 bug 让占位符漏过，在此捕获——
		// 占位符作命令名意味着运行时决定命令 → 不安全。
		if strings.Contains(name, cmdsubPlaceholder) ||
			strings.Contains(name, varPlaceholder) {
			return SemanticCheckResult{OK: false,
				Reason: "Command name is runtime-determined (placeholder argv[0])"}
		}

		// argv[0] 以算子/标志开头：这是片段非命令。可能是行继续泄漏或失误。
		if stringsHasPrefix(name, "-") || stringsHasPrefix(name, "|") ||
			stringsHasPrefix(name, "&") {
			return SemanticCheckResult{OK: false,
				Reason: "Command appears to be an incomplete fragment"}
		}

		// 安全：内部重新解析 NAME 操作数的内建。bash 在 NAME 位置算术
		// 求值 `arr[EXPR]`，即使 argv 元素来自单引号 raw_string（对
		// tree-sitter 是不透明叶子）也运行下标中的 $(cmd)。两种形态：
		// 分离（`printf -v NAME`）与融合（`printf -vNAME`，getopt 式）。
		// `printf '[%s]' x` 安全——`[` 在格式串中不在 -v 后。
		if dangerFlags, ok := subscriptEvalFlags[name]; ok {
			for i := 1; i < len(a); i++ {
				arg := a[i]
				// 分离形态：`-v` 后下一参数是 NAME。
				if dangerFlags[arg] && i+1 < len(a) &&
					strings.Contains(a[i+1], "[") {
					return SemanticCheckResult{OK: false,
						Reason: "'" + name + " " + arg + "' operand contains array subscript — bash evaluates $(cmd) in subscripts"}
				}
				// 组合短标志：`-ra` 是 `-r -a` 的 bash 速记。检查组合标志串
				// 中是否出现危险标志字符。危险标志的 NAME 操作数是下一参数。
				if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' &&
					!strings.Contains(arg, "[") {
					for flag := range dangerFlags {
						if len(flag) == 2 && strings.Contains(arg, flag[1:]) {
							if i+1 < len(a) && strings.Contains(a[i+1], "[") {
								return SemanticCheckResult{OK: false,
									Reason: "'" + name + " " + flag + "' (combined in '" + arg + "') operand contains array subscript — bash evaluates $(cmd) in subscripts"}
							}
						}
					}
				}
				// 融合形态：`-vNAME` 单参数。仅短选项标志融合（getopt），
				// 查 -v/-a/-R。`[[` 只有 test_operator 节点。
				for flag := range dangerFlags {
					if len(flag) == 2 && stringsHasPrefix(arg, flag) &&
						len(arg) > 2 && strings.Contains(arg, "[") {
						return SemanticCheckResult{OK: false,
							Reason: "'" + name + " " + flag + "' (fused) operand contains array subscript — bash evaluates $(cmd) in subscripts"}
					}
				}
			}
		}

		// 安全：`[[ ARG OP ARG ]]` 算术比较。bash 对两个操作数都按算术
		// 表达式求值，递归展开 `arr[$(cmd)]` 下标，即使来自单引号
		// raw_string。检查每个算术比较算子相邻的双侧操作数——
		// SUBSCRIPT_EVAL_FLAGS 的"标志后下一参数"模式无法表达"二元算子
		// 任一侧"。字符串比较（==/!=/=~）不触发算术求值——`[[ 'a[x]' == y ]]`
		// 是字面串比较。
		if name == "[[" {
			// i 从 2 起：a[0]='[['（含 '['），a[1] 是首个真实操作数。
			// 二元算子不会出现在 index 2 之前。
			for i := 2; i < len(a); i++ {
				if !testArithCmpOps[a[i]] {
					continue
				}
				if (i > 0 && strings.Contains(a[i-1], "[")) ||
					(i+1 < len(a) && strings.Contains(a[i+1], "[")) {
					return SemanticCheckResult{OK: false,
						Reason: "'[[ ... " + a[i] + " ... ]]' operand contains array subscript — bash arithmetically evaluates $(cmd) in subscripts"}
				}
			}
		}

		// 安全：`read`/`unset` 把每个裸位置参数当 NAME——无需标志。
		// `read 'a[$(id)]' <<< data` 即使 argv[1] 来自单引号 raw_string 且
		// 无 -a 标志也执行 id。与 SUBSCRIPT_EVAL_FLAGS 同一原语，触发条件
		// 是位置而非标志。跳过 read 取数据标志（-p PROMPT 等）的操作数
		// 避免封 `read -p '[foo] ' var`。
		if bareSubscriptNameBuiltins[name] {
			skipNext := false
			for i := 1; i < len(a); i++ {
				arg := a[i]
				if skipNext {
					skipNext = false
					continue
				}
				if len(arg) > 0 && arg[0] == '-' {
					if name == "read" {
						if readDataFlags[arg] {
							skipNext = true
						} else if len(arg) > 2 && arg[1] != '-' {
							// 组合短标志如 `-rp`。getopt 式：取数据标志字符
							// 消费参数余下部分作为其操作数（`-p[foo]` →
							// prompt=`[foo]`），若是最后一个字符则消费下一参数
							//（`-rp '[foo]'` → prompt=`[foo]`）。所以
							// skipNext 当且仅当数据标志字符出现在末尾、其前
							// 只有 -r/-s 等无参标志。
							for j := 1; j < len(arg); j++ {
								if readDataFlags["-"+string(arg[j])] {
									if j == len(arg)-1 {
										skipNext = true
									}
									break
								}
							}
						}
					}
					continue
				}
				if strings.Contains(arg, "[") {
					return SemanticCheckResult{OK: false,
						Reason: "'" + name + "' positional NAME '" + arg + "' contains array subscript — bash evaluates $(cmd) in subscripts"}
				}
			}
		}

		// 安全：shell 保留字作 argv[0] 表示 tree-sitter 误解析。
		// `! for i in a; do :; done` 解析为 `command "for i in a"` +
		// `command "do :"` + `command "done"`——tree-sitter 未识别 `!` 后的
		// `for` 是复合命令起始。拒收：保留字永远不可能是合法命令名，
		// ['do','false'] 类 argv 无意义。
		if isShellKeyword(name) {
			return SemanticCheckResult{OK: false,
				Reason: "Shell keyword '" + name + "' as command name — tree-sitter mis-parse"}
		}

		// 检查 argv（非 .text）以同时捕获单引号（'\n#'）与双引号（"\n#"）
		// 变体。env 值与重定向同属 .text span，同样的下游 bug 适用。
		// heredoc 体被排除在 argv 外，markdown `##` 标题不触发。
		// TODO 上游：待下游路径校验改为操作 argv 后移除。
		for _, arg := range cmd.Argv {
			if strings.Contains(arg, "\n") && newlineHashRe.MatchString(arg) {
				return SemanticCheckResult{OK: false,
					Reason: "Newline followed by # inside a quoted argument can hide arguments from path validation"}
			}
		}
		for _, ev := range cmd.EnvVars {
			if strings.Contains(ev.Value, "\n") && newlineHashRe.MatchString(ev.Value) {
				return SemanticCheckResult{OK: false,
					Reason: "Newline followed by # inside an env var value can hide arguments from path validation"}
			}
		}
		for _, r := range cmd.Redirects {
			if strings.Contains(r.Target, "\n") && newlineHashRe.MatchString(r.Target) {
				return SemanticCheckResult{OK: false,
					Reason: "Newline followed by # inside a redirect target can hide arguments from path validation"}
			}
		}

		// jq 的 system() 内建执行任意 shell 命令，--from-file 等 flags 可把
		// 任意文件读入 jq 变量。旧路径由 bashSecurity.ts 的
		// validateJqCommand 捕获，但该校验器 gated 于 `astSubcommands ===
		// null`，AST 解析成功时从不运行。此处镜像检查使 AST 路径有同样
		// 防御。
		if name == "jq" {
			for _, arg := range a {
				if regexpJqSystem.MatchString(arg) {
					return SemanticCheckResult{OK: false,
						Reason: "jq command contains system() function which executes arbitrary commands"}
				}
			}
			for _, arg := range a {
				if regexpJqDangerousFlags.MatchString(arg) {
					return SemanticCheckResult{OK: false,
						Reason: "jq command contains dangerous flags that could execute code or read arbitrary files"}
				}
			}
		}

		if zshDangerousBuiltins[name] {
			return SemanticCheckResult{OK: false,
				Reason: "Zsh builtin '" + name + "' can bypass security checks"}
		}

		if evalLikeBuiltins[name] {
			// `command -v foo` / `command -V foo` 是 POSIX 存在性检查——只
			// 打印路径，从不执行 argv[1]。裸 `command foo` 确实绕过函数/
			// alias 查找（关注点），保持封堵。
			if name == "command" && (len(a) > 1 && (a[1] == "-v" || a[1] == "-V")) {
				// 落入剩余检查
			} else if name == "fc" && !anyArgMatches(a[1:], regexpFcSafe) {
				// `fc -l`、`fc -ln` 列历史——安全。`fc -e ed` 调编辑器后执行。
				// `fc -s [pat=rep]` 重新执行上一条匹配命令（可带替换）——
				// 与 eval 同等危险。封任何含 e 或 s 的短选项，避免 `fc -l`
				// 误报。
			} else if name == "compgen" && !anyArgMatches(a[1:], regexpCompgenSafe) {
				// `compgen -c/-f/-v` 仅列补全——安全。`compgen -C cmd` 立即
				// 执行 cmd；`-F func` 调 shell 函数；`-W list` 词展开参数
				//（含单引号 raw_string 中的 $(cmd)）。封任何含 C/F/W 的短
				// 选项（大小写敏感：-c/-f 安全）。
			} else {
				return SemanticCheckResult{OK: false,
					Reason: "'" + name + "' evaluates arguments as shell code"}
			}
		}

		// /proc/*/environ 暴露其他进程的环境变量（含秘密）。检查 argv 与
		// 重定向目标——`cat /proc/self/environ` 与 `cat < /proc/self/environ`
		// 都读它。
		for _, arg := range cmd.Argv {
			if strings.Contains(arg, "/proc/") && procEnvironRe.MatchString(arg) {
				return SemanticCheckResult{OK: false,
					Reason: "Accesses /proc/*/environ which may expose secrets"}
			}
		}
		for _, r := range cmd.Redirects {
			if strings.Contains(r.Target, "/proc/") && procEnvironRe.MatchString(r.Target) {
				return SemanticCheckResult{OK: false,
					Reason: "Accesses /proc/*/environ which may expose secrets"}
			}
		}
	}
	return SemanticCheckResult{OK: true}
}
