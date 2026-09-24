// ast_walk_stmt.go——上游 eva-cli utils/bash/ast.ts 的语句级处理器
//（579-676 declaration_command、693-880 for/if/while、957-1009
// test 表达式、1365-1393 命令替换递归）的 Go 直译。

package parser

// walkDeclarationCommand export/local/readonly/declare/typeset 处理
// （对齐 579-676 行）。tree-sitter 产为 declaration_command 而非 command，
// 此前落 too-complex。值经 walkVariableAssignment 校验：值中 $() 递归
// 提取（内层命令进 commands，外层 argv 得占位符）；其余不允许的展开
// 仍经 walkArgument 拒收。argv[0] 是内建名，`Bash(export:*)` 规则可匹配。
func walkDeclarationCommand(
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
		case "export", "local", "readonly", "declare", "typeset":
			argv = append(argv, child.Text)
		case "word", "number", "raw_string", "string", "concatenation":
			// 标志（declare -r）、引号名（export "FOO=bar"）、数字
			//（declare -i 42）。镜像 walkCommand 的 argv 处理——此前
			// `export "FOO=bar"` 在 string 子节点上误判 too-complex。
			// walkArgument 校验各参数（展开仍拒收）。
			arg, errRes := walkArgument(child, commands, varScope)
			if errRes != nil {
				return errRes
			}
			// 安全：declare/typeset/local 改变赋值语义的标志破坏静态模型。
			// -n（nameref）：`declare -n X=Y` 后 $X 解引用到 $Y 的值——
			// varScope 存的是目标名 Y，argv[0] 显示 Y 而 bash 运行 $Y 所持
			// 内容。-i（整数）：`declare -i X='a[$(cmd)]'` 赋值期算术求值，
			// 即使单引号 raw_string 也执行 $(cmd)。-a/-A（数组）：赋值时
			// 下标算术。-r/-x/-g/-p/-f/-F 惰性。检查解析后的 arg（非
			// child.text）——`\-n` 与带引号 `-n` 都被捕获。限定
			// declare/typeset/local：`export -n` 意为移除 export 属性
			//（非 nameref），export/readonly 不接受 -i；readonly -a/-A
			// 会因下标参数非法标识符而拒。
			if (argv[0] == "declare" || argv[0] == "typeset" ||
				argv[0] == "local") && regexpFlagNiaA(arg) {
				return &ParseForSecurityResult{
					Kind:     kindTooComplex,
					Reason:   "declare flag " + arg + " changes assignment semantics (nameref/integer/array)",
					NodeType: "declaration_command",
				}
			}
			// 安全：带下标的裸位置赋值同样求值——无需 -a/-i 标志。
			// `declare 'x[$(id)]=val'` 隐式创建数组元素，算术求值下标运行
			// $(id)。tree-sitter 把单引号形式产为 raw_string 叶子，
			// walkArgument 只见字面文本。限定 declare/typeset/local：
			// export/readonly 在标识符校验时拒 `[`。
			if (argv[0] == "declare" || argv[0] == "typeset" ||
				argv[0] == "local") &&
				len(arg) > 0 && arg[0] != '-' && containsSubscript(arg) {
				return &ParseForSecurityResult{
					Kind:     kindTooComplex,
					Reason:   "declare positional '" + arg + "' contains array subscript — bash evaluates $(cmd) in subscripts",
					NodeType: "declaration_command",
				}
			}
			argv = append(argv, arg)
		case "variable_assignment":
			ev, errRes := walkVariableAssignment(child, commands, varScope)
			if errRes != nil {
				return errRes
			}
			// export/declare 赋值进 scope，后续 $VAR 引用可解析。
			applyVarToScope(varScope, ev)
			argv = append(argv, ev.name+"="+ev.value)
		case "variable_name":
			// `export FOO`——裸名，无赋值。
			argv = append(argv, child.Text)
		default:
			return tooComplexPtr(child)
		}
	}
	*commands = append(*commands, SimpleCommand{
		Argv: argv, EnvVars: []EnvVar{}, Redirects: []Redirect{}, Text: node.Text,
	})
	return nil
}

// walkForStatement `for VAR in WORD...; do BODY; done`（对齐 693-762 行）。
// 体命令提取一次；每次迭代运行相同命令。
//
// 安全：循环变量恒为未知值（VAR_PLACEHOLDER）。即使"静态"迭代词也可能是：
//   - 绝对路径：`for i in /etc/passwd; do rm $i; done`——体 argv 是占位符，
//     路径校验看不到 /etc/passwd
//   - glob：`for i in /etc/*; do rm $i; done`——解析时静态、运行时展开
//   - 标志：`for i in -rf /; do rm $i; done`——标志走私
//
// VAR_PLACEHOLDER 意味着体中裸 `$i` → too-complex。仅字符串内嵌
// （echo "item: $i"）保持 simple。
func walkForStatement(
	node *TsNode,
	commands *[]SimpleCommand,
	varScope map[string]string,
) *ParseForSecurityResult {
	loopVar := ""
	haveLoopVar := false
	var doGroup *TsNode
	for _, child := range node.Children {
		if child == nil {
			continue
		}
		if child.Type == "variable_name" {
			loopVar = child.Text
			haveLoopVar = true
		} else if child.Type == "do_group" {
			doGroup = child
		} else if child.Type == "for" || child.Type == "in" ||
			child.Type == "select" || child.Type == ";" {
			continue // 结构 token
		} else if child.Type == "command_substitution" {
			// `for i in $(seq 1 3)`——内层命令被提取并规则检查。
			if err := collectCommandSubstitution(child, commands, varScope); err != nil {
				return err
			}
		} else {
			// 迭代值——经 walkArgument 校验。值丢弃：体 argv 无视迭代词
			// 恒得 VAR_PLACEHOLDER，体中裸 `$i` → too-complex。仍校验以
			// 拒收迭代词本身为不允许展开的形态（如 `for i in $(cmd); do...`）。
			if _, errRes := walkArgument(child, commands, varScope); errRes != nil {
				return errRes
			}
		}
	}
	if !haveLoopVar || doGroup == nil {
		return tooComplexPtr(node)
	}
	// 安全：`for PS4 in '$(id)'; do set -x; :; done` 经下方 varScope 直接
	// 设 PS4——walkVariableAssignment 的 PS4/IFS 检查不触发。追踪期 RCE
	//（PS4）或词分割绕过（IFS）。无合法用例。
	if loopVar == "PS4" || loopVar == "IFS" {
		return &ParseForSecurityResult{
			Kind:     kindTooComplex,
			Reason:   loopVar + " as loop variable bypasses assignment validation",
			NodeType: "for_statement",
		}
	}
	// 安全：体使用 scope 拷贝——循环体内赋值的变量不泄漏到 done 之后的
	// 命令。循环变量本身设在真 scope（bash 语义：$i 循环后仍设置）并
	// 拷贝进体 scope。恒 VAR_PLACEHOLDER（见上）。
	varScope[loopVar] = varPlaceholder
	bodyScope := copyScope(varScope)
	for _, c := range doGroup.Children {
		if c == nil {
			continue
		}
		if c.Type == "do" || c.Type == "done" || c.Type == ";" {
			continue
		}
		if err := collectCommands(c, commands, bodyScope); err != nil {
			return err
		}
	}
	return nil
}

// walkIfWhileStatement `if COND; then BODY; [elif/else] fi` 与
// `while COND; do BODY; done`（对齐 764-880 行）。提取条件命令与全部
// 分支/体命令，全部过权限规则。`while read VAR` 追踪 VAR 供体引用。
//
// 安全：分支体用 scope 拷贝——条件分支（可能不执行）内的赋值不得泄漏
// 到 fi/done 之后的命令。`if false; then T=safe; fi && rm $T` 必须拒 $T。
// 条件命令用真 varScope（检查时恒运行，赋值无条件——如 `while read V`
// 追踪须持久到体拷贝）。
//
// tree-sitter if_statement 子序：if, COND..., then, THEN-BODY...,
// [elif_clause...], [else_clause], fi。以是否见过 then token 区分条件
// 与体。
func walkIfWhileStatement(
	node *TsNode,
	commands *[]SimpleCommand,
	varScope map[string]string,
) *ParseForSecurityResult {
	seenThen := false
	for _, child := range node.Children {
		if child == nil {
			continue
		}
		if child.Type == "if" || child.Type == "fi" || child.Type == "else" ||
			child.Type == "elif" || child.Type == "while" ||
			child.Type == "until" || child.Type == ";" {
			continue
		}
		if child.Type == "then" {
			seenThen = true
			continue
		}
		if child.Type == "do_group" {
			// while 体：scope 拷贝递归（体赋值不越 done 泄漏）。拷贝含
			// 条件中 `read VAR` 的追踪（此时已在真 varScope）。
			bodyScope := copyScope(varScope)
			for _, c := range child.Children {
				if c == nil {
					continue
				}
				if c.Type == "do" || c.Type == "done" || c.Type == ";" {
					continue
				}
				if err := collectCommands(c, commands, bodyScope); err != nil {
					return err
				}
			}
			continue
		}
		if child.Type == "elif_clause" || child.Type == "else_clause" {
			// elif_clause: elif, cond, ;, then, body... /
			// else_clause: else, body...。scope 拷贝——elif/else 分支赋值
			// 不越 fi 泄漏。
			branchScope := copyScope(varScope)
			for _, c := range child.Children {
				if c == nil {
					continue
				}
				if c.Type == "elif" || c.Type == "else" ||
					c.Type == "then" || c.Type == ";" {
					continue
				}
				if err := collectCommands(c, commands, branchScope); err != nil {
					return err
				}
			}
			continue
		}
		// 条件（seenThen=false）或 then 体（seenThen=true）。条件用真
		// varScope（恒运行）。then 体用拷贝。特例 `while read VAR`：条件
		// 中 `read VAR` 收集后在真 scope 追踪 VAR 使体拷贝继承。
		var targetScope map[string]string
		if seenThen {
			targetScope = copyScope(varScope)
		} else {
			targetScope = varScope
		}
		before := len(*commands)
		if err := collectCommands(child, commands, targetScope); err != nil {
			return err
		}
		// 条件含 `read VAR...` 时在真 scope 追踪变量。read 的值未知
		//（stdin 输入）→ VAR_PLACEHOLDER（未知值哨兵，仅字符串内）。
		if !seenThen {
			for i := before; i < len(*commands); i++ {
				c := (*commands)[i]
				if len(c.Argv) > 0 && c.Argv[0] == "read" {
					for _, a := range c.Argv[1:] {
						// 跳过标志（-r、-d 等）；追踪裸标识符参数为变量名。
						if !stringsHasPrefix(a, "-") && isIdentifier(a) {
							// 安全：commands[] 是扁平累加器。条件中
							// `true || read VAR`：list 处理器正确对 || 右侧
							// 用 scope 拷贝（可能不运行），但 `read VAR` 仍被
							// 推入 commands——从此处无法得知它被作用域隔离。
							// 同理 `echo | read VAR`（管道，bash 子 shell）与
							// `(read VAR)`。用占位符覆盖已跟踪字面量会隐藏
							// 路径穿越：`VAR=../../etc/passwd && if true ||
							// read VAR; then cat "/tmp/$VAR"; fi`——解析器看到
							// /tmp/__TRACKED_VAR__，bash 读 /etc/passwd。
							// 已跟踪字面量将被覆盖时 fail closed。安全场景
							//（无前值或已是占位符）→ 继续。
							if existing, ok := varScope[a]; ok &&
								!containsAnyPlaceholder(existing) {
								return &ParseForSecurityResult{
									Kind:     kindTooComplex,
									Reason:   "'read " + a + "' in condition may not execute (||/pipeline/subshell); cannot prove it overwrites tracked literal '" + existing + "'",
									NodeType: "if_statement",
								}
							}
							varScope[a] = varPlaceholder
						}
					}
				}
			}
		}
	}
	return nil
}

// walkTestExpr 递归走 test_command 表达式树（对齐 walkTestExpr）：
// unary/binary/negated/parenthesized expression。叶子是 test_operator
// token（-f, -d, ==）与操作数（word/string/number 等），操作数经
// walkArgument 校验。
func walkTestExpr(
	node *TsNode,
	argv *[]string,
	innerCommands *[]SimpleCommand,
	varScope map[string]string,
) *ParseForSecurityResult {
	switch node.Type {
	case "unary_expression", "binary_expression", "negated_expression",
		"parenthesized_expression":
		for _, c := range node.Children {
			if c == nil {
				continue
			}
			if err := walkTestExpr(c, argv, innerCommands, varScope); err != nil {
				return err
			}
		}
		return nil
	case "test_operator", "!", "(", ")", "&&", "||",
		"==", "=", "!=", "<", ">", "=~":
		*argv = append(*argv, node.Text)
		return nil
	case "regex", "extglob_pattern":
		// =~ 或 ==/!= 的 RHS 模式文本——无代码执行。解析器产为无子节点的
		// 叶子（模式内的 $(...)/${...} 是兄弟节点，单独走树）。
		*argv = append(*argv, node.Text)
		return nil
	default:
		// 操作数——word、string、number 等。经 walkArgument 校验。
		arg, errRes := walkArgument(node, innerCommands, varScope)
		if errRes != nil {
			return errRes
		}
		*argv = append(*argv, arg)
		return nil
	}
}

// collectCommandSubstitution 递归进入 command_substitution 节点的内层
// 命令（对齐 collectCommandSubstitution）。内层命令解析干净（simple）
// 则加入 innerCommands 并返回 nil；内层自身 too-complex（如嵌套算术
// 展开、进程替换）则传错误。`echo $(git rev-parse HEAD)` 提取外层
// `echo $(...)` 与内层 `git rev-parse HEAD` 两条——权限规则须同时匹配。
func collectCommandSubstitution(
	csNode *TsNode,
	innerCommands *[]SimpleCommand,
	varScope map[string]string,
) *ParseForSecurityResult {
	// $() 之前设置的变量在内部可见（bash 子 shell 语义），内部设置的不
	// 外漏。传外层 scope 拷贝，内层赋值不改动外层 map。
	innerScope := copyScope(varScope)
	// command_substitution children：`$(` 或反引号、内层语句、`)`
	for _, child := range csNode.Children {
		if child == nil {
			continue
		}
		if child.Type == "$(" || child.Type == "`" || child.Type == ")" {
			continue
		}
		if err := collectCommands(child, innerCommands, innerScope); err != nil {
			return err
		}
	}
	return nil
}
