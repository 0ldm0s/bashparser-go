// ast_semantics_data.go——上游 eva-cli utils/bash/ast.ts 语义检查的
// 数据集合与正则常量（2043-2219 行：ZSH_DANGEROUS_BUILTINS/
// EVAL_LIKE_BUILTINS/SUBSCRIPT_EVAL_FLAGS/TEST_ARITH_CMP_OPS/
// BARE_SUBSCRIPT_NAME_BUILTINS/READ_DATA_FLAGS/PROC_ENVIRON_RE/
// NEWLINE_HASH_RE 等）。

package parser

import "regexp"

// zshDangerousBuiltins zsh 模块内建（对齐 ZSH_DANGEROUS_BUILTINS）。
// 它们不是 PATH 上的二进制——是经 zmodload 加载的 zsh 内部。BashTool 经
// 用户默认 shell（常为 zsh）运行，这些解析为普通 command 节点无区分
// 语法，只能按名捕获。
var zshDangerousBuiltins = map[string]bool{
	"zmodload": true, "emulate": true, "sysopen": true, "sysread": true,
	"syswrite": true, "sysseek": true, "zpty": true, "ztcp": true,
	"zsocket": true, "zf_rm": true, "zf_mv": true, "zf_ln": true,
	"zf_chmod": true, "zf_chown": true, "zf_mkdir": true, "zf_rmdir": true,
	"zf_chgrp": true,
}

// evalLikeBuiltins 把参数求值为代码或以其他方式逃逸 argv 抽象的内建
// （对齐 EVAL_LIKE_BUILTINS）。`eval "rm -rf /"` 的 argv
// ['eval','rm -rf /'] 对标志校验看似惰性但执行该串。与命令替换同待遇。
var evalLikeBuiltins = map[string]bool{
	"eval": true, "source": true, ".": true, "exec": true,
	"command": true, "builtin": true, "fc": true,
	// `coproc rm -rf /` 以协程方式 spawn rm。tree-sitter 解析为普通命令
	// argv[0]='coproc'，权限规则与路径校验会检查 'coproc' 而非 'rm'。
	"coproc": true,
	// zsh 前置修饰符：`noglob cmd args` 关闭 glob 运行 cmd。解析为普通
	// 命令（noglob 是 argv[0]，真实命令是 argv[1]），按 argv[0] 的权限
	// 匹配会看到 'noglob' 而非被包装命令。
	"noglob": true, "nocorrect": true,
	// `trap 'cmd' SIGNAL`——cmd 在信号/退出时作为 shell 代码运行。EXIT
	// 在每次 BashTool 调用结束时触发——保证执行。
	"trap": true,
	// `enable -f /path/lib.so name`——dlopen 任意 .so 为内建。原生代码执行。
	"enable": true,
	// `mapfile -C callback -c N` / `readarray -C callback`——callback 每
	// N 输入行作为 shell 代码运行。
	"mapfile": true, "readarray": true,
	// `hash -p /path cmd`——污染 bash 命令查找缓存。同命令中后续 `cmd`
	// 解析为 /path 而非 PATH 查找。
	"hash": true,
	// `bind -x '"key":cmd'` / `complete -C cmd`——仅交互式回调但仍是
	// 代码串参数。非交互 BashTool shell 中低影响，为一致性封。
	// `compgen -C cmd` 不是仅交互式：它立即执行 -C 参数生成补全。
	"bind": true, "complete": true, "compgen": true,
	// `alias name='cmd'`——非交互 bash 默认不展开别名，但 `shopt -s
	// expand_aliases` 可启用。同为纵深防御（alias 后同命令使用该名）。
	"alias": true,
	// `let EXPR` 算术求值 EXPR——与 $(( EXPR )) 相同。表达式中的数组
	// 下标即使参数来自单引号 raw_string 也展开 $(cmd)：`let 'x=a[$(id)]'`
	// 执行 id。tree-sitter 视 raw_string 为不透明叶子。与 walkArithmetic
	// 相同的防御原语，但 `let` 是普通命令节点。
	"let": true,
}

// subscriptEvalFlags 内部重新解析 NAME 操作数并算术求值 `arr[EXPR]`
// 下标的内建——即使 argv 元素来自单引号 raw_string，下标中的 $(cmd)
// 也会运行。`test -v 'a[$(id)]'` → tree-sitter 见不透明叶子，bash 跑 id。
// 映射：内建名 → 其下一个参数为 NAME 的标志集（对齐 SUBSCRIPT_EVAL_FLAGS）。
var subscriptEvalFlags = map[string]map[string]bool{
	"test":   {"-v": true, "-R": true},
	"[":      {"-v": true, "-R": true},
	"[[":     {"-v": true, "-R": true},
	"printf": {"-v": true},
	"read":   {"-a": true},
	"unset":  {"-v": true},
	// bash 5.1+：`wait -p VAR [id...]` 把被等待 PID 存入 VAR。VAR 为
	// `arr[EXPR]` 时 bash 算术求值下标——即使参数来自单引号 raw_string
	// 也运行 $(cmd)。bash 5.3.9 实证：`: & wait -p 'a[$(id)]' %1` 执行 id。
	"wait": {"-p": true},
}

// testArithCmpOps `[[ ARG1 OP ARG2 ]]` 算术比较算子（对齐
// TEST_ARITH_CMP_OPS）。bash 手册："用于 [[ 时 Arg1 与 Arg2 按算术
// 表达式求值"。算术求值递归展开数组下标——`[[ 'a[$(id)]' -eq 0 ]]`
// 即使操作数是单引号 raw_string 不透明叶子也执行 id。-v/-R（一元，
// 标志后 NAME）之外这些是二元的——下标可出现在任一侧，
// SUBSCRIPT_EVAL_FLAGS 的"下一参数"逻辑不足。`[`/`test` 不受影响
// （bash 报 "integer expression expected"），但 test_command 处理器把
// 两种形态的 argv[0] 归一为 '[['，它们也获得本检查——轻度过度封堵，
// 安全侧。
var testArithCmpOps = map[string]bool{
	"-eq": true, "-ne": true, "-lt": true, "-le": true, "-gt": true, "-ge": true,
}

// bareSubscriptNameBuiltins 每个非标志位置参数都是 NAME、bash 内部重新
// 解析并算术求值下标的内建——无需标志。`read 'a[$(id)]' <<< data` 执行
// id：每个位置参数都是要赋值的变量名，`arr[EXPR]` 在该位置是合法语法。
// `unset NAME...` 相同（tree-sitter 的 unset_command 处理器当前在到达
// 此处前已拒 raw_string 子节点——此为纵深防御）。printf 例外（位置参数
// 是 FORMAT/数据），test/[（操作数是值，仅 -v/-R 取 NAME）。
// declare/typeset/local 在 declaration_command 处理，不会以普通命令到达。
var bareSubscriptNameBuiltins = map[string]bool{"read": true, "unset": true}

// readDataFlags `read` 的下一参数为数据（提示/定界/计数/fd）而非 NAME
// 的标志。`read -p '[foo] ' var` 不得因提示串中的 `[` 触发。-a 刻意
// 不在集合——其操作数就是 NAME（对齐 READ_DATA_FLAGS）。
var readDataFlags = map[string]bool{
	"-p": true, "-d": true, "-n": true, "-N": true, "-t": true, "-u": true, "-i": true,
}

// isShellKeyword shell 保留字作 argv[0] = tree-sitter 误解析信号
// （对齐 2542-2552 的 SHELL_KEYWORDS 检查；集合本体为 bashParser.ts
// 87 行——Go 侧同包 shellKeywords）。保留字永远不可能是合法 argv[0]；
// 出现即解析器把复合命令误解析为普通命令，拒收避免无意义 argv 下行。
func isShellKeyword(name string) bool { return shellKeywords[name] }

// regexpJqDangerousFlags 对齐 jq 危险 flags：
// /^(?:-[fL](?:$|[^A-Za-z])|--(?:from-file|rawfile|slurpfile|library-path)(?:$|=))/
var regexpJqDangerousFlags = regexp.MustCompile(
	`^(?:-[fL](?:$|[^A-Za-z])|--(?:from-file|rawfile|slurpfile|library-path)(?:$|=))`)

// timeoutFlagValueRe timeout 标志值白名单（对齐 TIMEOUT_FLAG_VALUE_RE）：
// 信号是 TERM/KILL/9，时长是 5/5s/10.5。拒 $ ( ) ` | ; & 与此前经
// [^ \t]+ 匹配的换行——`timeout -k$(id) 10 ls` 绝不能剥离。
var timeoutFlagValueRe = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)

// regexpTimeoutFusedLong 对齐 /^--(?:kill-after|signal)=[A-Za-z0-9_.+-]+$/。
var regexpTimeoutFusedLong = regexp.MustCompile(`^--(?:kill-after|signal)=[A-Za-z0-9_.+-]+$`)

// regexpTimeoutFusedShort 对齐 /^-[ks][A-Za-z0-9_.+-]+$/。
var regexpTimeoutFusedShort = regexp.MustCompile(`^-[ks][A-Za-z0-9_.+-]+$`)

// regexpDuration 对齐 /^\d+(?:\.\d+)?[smhd]?$/。
var regexpDuration = regexp.MustCompile(`^\d+(?:\.\d+)?[smhd]?$`)

// regexpInt 对齐 /^-?\d+$/（nice -N）。
var regexpInt = regexp.MustCompile(`^-?\d+$`)

// regexpNegInt 对齐 /^-\d+$/（nice -10）。
var regexpNegInt = regexp.MustCompile(`^-\d+$`)

// regexpDollarBacktick 对齐 /[$(`]/（nice 参数含展开）。
var regexpDollarBacktick = regexp.MustCompile("[$(`]")

// stdbuf 标志形态（对齐 ast.ts STDBUF_SHORT_SEP_RE/SHORT_FUSED/LONG，
// 113-115 行定义、checkSemantics 消费）。
var (
	stdbufShortSepRe   = regexp.MustCompile(`^-[ioe]$`)
	stdbufShortFusedRe = regexp.MustCompile(`^-[ioe].`)
	stdbufLongRe       = regexp.MustCompile(`^--(input|output|error)=`)
)

// regexpFcSafe 对齐 /^-[^-]*[es]/——fc 含 e/s 的短选项（执行语义）。
var regexpFcSafe = regexp.MustCompile(`^-[^-]*[es]`)

// regexpCompgenSafe 对齐 /^-[^-]*[CFW]/——compgen 含 C/F/W 的短选项。
var regexpCompgenSafe = regexp.MustCompile(`^-[^-]*[CFW]`)

// anyArgMatches 任一参数匹配正则（对齐 a.slice(1).some(...)）。
func anyArgMatches(args []string, re *regexp.Regexp) bool {
	for _, arg := range args {
		if re.MatchString(arg) {
			return true
		}
	}
	return false
}
