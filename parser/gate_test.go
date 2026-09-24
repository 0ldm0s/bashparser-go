package parser

import (
	"strings"
	"testing"
)

// TestParseCommandRawStates 三态语义（对齐 parser.ts parseCommandRaw）
func TestParseCommandRawStates(t *testing.T) {
	// 空命令 → 不可用（可降级）
	if _, st := ParseCommandRaw(""); st != GateUnavailable {
		t.Errorf("空命令应为 GateUnavailable，得 %v", st)
	}
	// 超长 → 不可用（10000 门，对齐 MAX_COMMAND_LENGTH）
	long := strings.Repeat("a", 10001)
	if _, st := ParseCommandRaw(long); st != GateUnavailable {
		t.Errorf("超长命令应为 GateUnavailable，得 %v", st)
	}
	// 恰好 10000 字符 → 不超门
	ok := strings.Repeat("a", 10000)
	if _, st := ParseCommandRaw(ok); st != GateParsed {
		t.Errorf("10000 字符应可解析，得 %v", st)
	}
	// 正常命令 → 成功
	root, st := ParseCommandRaw("echo hello")
	if st != GateParsed || root == nil || root.Type != "program" {
		t.Fatalf("正常命令应 GateParsed 且根为 program，得 %v root=%v", st, root)
	}
	// 病理输入（对齐 parser.ts 注释：(( a[0][0]... )) 深下标形态）。
	// 运行时差异：上游 V8/WASM 默认 50ms 即中止（约 2800 下标）；Go 原生
	// 速度下门内输入 50ms 内轻松完成——用门内最大规模 + 收紧至 1ms
	// 验证 Aborted 分支映射（fail-closed 契约不变）。
	pathological := "((" + strings.Repeat("a[0][0]", 1400) + "))"
	if len(pathological) > 10000 {
		t.Fatalf("测试输入须在长度门内，当前 %d", len(pathological))
	}
	if _, st := parseCommandRawChecked(pathological, 1); st != GateAborted {
		t.Errorf("收紧预算下病理输入应 GateAborted，得 %v", st)
	}
}

// TestParseCommandFolded 折叠语义（对齐 parseCommand 的 catch → null）
func TestParseCommandFolded(t *testing.T) {
	if ParseCommand("") != nil {
		t.Error("空命令应折叠为 nil")
	}
	if ParseCommand(strings.Repeat("a", 10001)) != nil {
		t.Error("超长应折叠为 nil")
	}
	if d := ParseCommand("echo hello"); d == nil || d.CommandNode == nil {
		t.Fatal("正常命令应解析成功且含 commandNode")
	}
}

// TestFindCommandNode 命令节点查找（对齐 findCommandNode 各分支）
func TestFindCommandNode(t *testing.T) {
	cases := []struct {
		cmd string
	}{
		{"echo hello"},
		{"FOO=bar echo baz"},   // variable_assignment 后跟 command
		{"echo a | grep b"},    // pipeline 递归
		{"echo a > /dev/null"}, // redirected_statement 内找 command
		{"cmd1 && cmd2"},       // list 内
	}
	for _, c := range cases {
		d := ParseCommand(c.cmd)
		if d == nil {
			t.Fatalf("%q 解析失败", c.cmd)
		}
		if d.CommandNode == nil {
			t.Errorf("%q 应找到 commandNode", c.cmd)
		}
	}
}

// TestExtractEnvVars 环境变量前缀提取（对齐 extractEnvVars）
func TestExtractEnvVars(t *testing.T) {
	d := ParseCommand("FOO=1 BAR=2 echo baz")
	if d == nil {
		t.Fatal("解析失败")
	}
	got := ExtractEnvVars(d.CommandNode)
	want := []string{"FOO=1", "BAR=2"}
	if len(got) != len(want) {
		t.Fatalf("env 提取应得 %v，得 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("env[%d] 应 %q，得 %q", i, want[i], got[i])
		}
	}
	// 无 env 前缀
	d2 := ParseCommand("echo baz")
	if got2 := ExtractEnvVars(d2.CommandNode); len(got2) != 0 {
		t.Errorf("无前缀应得空 env，得 %v", got2)
	}
}

// TestExtractCommandArguments 参数提取（对齐 extractCommandArguments）
func TestExtractCommandArguments(t *testing.T) {
	// 引号剥离
	d := ParseCommand(`echo 'single' "double" 42`)
	if d == nil {
		t.Fatal("解析失败")
	}
	got := ExtractCommandArguments(d.CommandNode)
	want := []string{"echo", "single", "double", "42"}
	if len(got) != len(want) {
		t.Fatalf("args 应得 %v，得 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("args[%d] 应 %q，得 %q", i, want[i], got[i])
		}
	}

	// 声明命令
	d2 := ParseCommand("export A=1 B")
	if d2 == nil {
		t.Fatal("解析失败")
	}
	if got2 := ExtractCommandArguments(d2.CommandNode); len(got2) != 1 || got2[0] != "export" {
		t.Errorf("声明命令应得 [export]，得 %v", got2)
	}

	// 遇命令替换停
	d3 := ParseCommand("echo $(cmd) after")
	if d3 == nil {
		t.Fatal("解析失败")
	}
	got3 := ExtractCommandArguments(d3.CommandNode)
	if len(got3) != 1 || got3[0] != "echo" {
		t.Errorf("遇替换应停在 [echo]，得 %v", got3)
	}
}
