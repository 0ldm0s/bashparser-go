// commands_split_test.go——commands.ts 拆分族行为测试（用例源自上游
// commands.ts/heredoc.ts 安全注释中的走私样例与 bash 语义要点）。

package parser

import (
	"reflect"
	"testing"
)

// TestSplitCommandWithOperatorsBasic 基本拆分：操作符分段、引号保真、
// 管道分段。
func TestSplitCommandWithOperatorsBasic(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"echo hi", []string{"echo hi"}},
		{"echo a && echo b", []string{"echo a", "&&", "echo b"}},
		{"echo a;echo b", []string{"echo a", ";", "echo b"}},
		{"cat f | grep x", []string{"cat f", "|", "grep x"}},
		// 分号在双引号内非操作符——相邻折叠合并为单段（占位符前缀法
		// 还原后引号保真）
		{`echo "a; b"`, []string{`echo "a; b"`}},
		{"", []string{}},
	}
	for _, tc := range cases {
		got := SplitCommandWithOperators(tc.cmd)
		if len(got) == 0 && len(tc.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitCommandWithOperators(%q)\n 得到 %q\n 期望 %q", tc.cmd, got, tc.want)
		}
	}
}

// TestSplitCommandHeredoc heredoc 预提取 + 还原（拆分不含体的碎片）。
func TestSplitCommandHeredoc(t *testing.T) {
	cmd := "cat <<EOF && echo done\nbody\nEOF"
	got := SplitCommandWithOperators(cmd)
	joined := ""
	for _, p := range got {
		joined += p + "|"
	}
	// 体被占位符替代——body 不应作为独立段出现；操作符分段正确
	if len(got) != 3 || got[1] != "&&" {
		t.Errorf("heredoc 命令拆分应保操作符分段：%q", got)
	}
	for _, p := range got {
		if p == "body" {
			t.Errorf("体内容不应独立成段：%q", got)
		}
	}
}

// TestSplitCommandLineContinuation 行连续合并（奇数反斜杠合并、偶数
// 不合并——上游 96-120 安全语义）。
func TestSplitCommandLineContinuation(t *testing.T) {
	// 奇数：换行被合并（消除换行分隔——无操作符时合并为单命令）
	got := SplitCommandWithOperators("tr a\\\nb")
	if len(got) != 1 || got[0] != "tr ab" {
		t.Errorf("奇数反斜杠连续行应合并为单命令，得到 %q", got)
	}
	// 奇数连续 + 操作符：合并消除换行后 && 仍分段（与 bash 执行一致）
	got = SplitCommandWithOperators("echo a\\\n&& echo b")
	if len(got) != 3 {
		t.Errorf("连续行合并后操作符仍应分段，得到 %q", got)
	}
	// 偶数：换行是分隔符——两命令
	got = SplitCommandWithOperators("echo a\\\\\nrm -rf /")
	if len(got) < 2 {
		t.Errorf("偶数反斜杠换行应为分隔符（rm 须可见），得到 %q", got)
	}
}

// TestSplitCommandParseFailBail 解析失败整体回退（${} 畸形形态）。
func TestSplitCommandParseFailBail(t *testing.T) {
	got := SplitCommandWithOperators(`echo ${}`)
	if len(got) != 1 {
		t.Errorf("解析失败应整体回退单元素，得到 %q", got)
	}
}

// TestSplitCommandDeprecated 重定向剥离（对齐 splitCommand_DEPRECATED
// 四形态）。
func TestSplitCommandDeprecated(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		// 2>&1 紧凑形态
		{"echo a 2>&1", []string{"echo a"}},
		// > file 静态目标
		{"echo a > out.txt", []string{"echo a"}},
		// >> 静态目标
		{"echo a >> out.txt", []string{"echo a"}},
		// FD 前缀剥离（echo foo 2 > file → echo foo）
		{"echo foo 2 > out.txt", []string{"echo foo"}},
		// 动态目标不剥（保留可见）——重定向操作符被 filterControlOperators
		// 过滤，仅目标保留
		{"echo a > $HOME/x", []string{"echo a", "$HOME/x"}},
		// /dev/null 2>&1 混合形态
		{"cmd > /dev/null 2>&1", []string{"cmd"}},
	}
	for _, tc := range cases {
		got := SplitCommandDeprecated(tc.cmd)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitCommandDeprecated(%q)\n 得到 %q\n 期望 %q", tc.cmd, got, tc.want)
		}
	}
}

// TestIsStaticRedirectTarget 静态目标判定。
func TestIsStaticRedirectTarget(t *testing.T) {
	positives := []string{"out.txt", "/tmp/output", "log-2026.txt"}
	for _, p := range positives {
		if !IsStaticRedirectTarget(p) {
			t.Errorf("%q 应为静态目标", p)
		}
	}
	negatives := []string{"", "a b", `$HOME/x`, "`cmd`", "*.txt", "a?b", "[ab]", "{a,b}",
		"~/x", ">(cmd)", "<(cmd)", "&1", "#file", "!hist", "=cmd"}
	for _, n := range negatives {
		if IsStaticRedirectTarget(n) {
			t.Errorf("%q 不应为静态目标", n)
		}
	}
}

// TestIsCommandList 纯命令列表判定。
func TestIsCommandList(t *testing.T) {
	if !IsCommandList("echo a && ls b | wc") {
		t.Errorf("string/glob/列表分隔符构成纯命令列表")
	}
	if !IsCommandList("cmd > out") {
		t.Errorf("输出重定向由 pathValidation 校验，列表仍纯")
	}
	// IsCommandList 仅做形态检查（string 段内容不参与判定）——
	// "rm -rf / x" 是 string 段，此形态为纯列表；内容级危险由其他
	// 判定链负责
	if !IsCommandList("echo a && rm -rf / x") {
		t.Errorf("纯形态（string/分隔符）应为命令列表")
	}
	if !IsCommandList(`echo "a;evil"`) {
		// 分号在引号内非操作符——单命令纯列表
		t.Errorf("引号内分号的单命令应为纯列表")
	}
}

// TestIsUnsafeCompoundCommand legacy 危险复合判定。
func TestIsUnsafeCompoundCommand(t *testing.T) {
	if IsUnsafeCompoundCommandDeprecated("echo hi") {
		t.Errorf("单命令非危险复合")
	}
	if IsUnsafeCompoundCommandDeprecated("echo a && echo b") {
		t.Errorf("纯命令列表非危险复合")
	}
	if !IsUnsafeCompoundCommandDeprecated(`echo ${}`) {
		t.Errorf("解析失败恒不安全（纵深防御）")
	}
}
