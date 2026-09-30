// commands_redirect_test.go——extractOutputRedirections 族行为测试
// （用例源自上游 commands.ts 安全注释走私样例与 bash 语义要点）。

package parser

import (
	"reflect"
	"strings"
	"testing"
)

// TestExtractRedirectionsBasic 基本提取：写/追加、重建保真。
func TestExtractRedirectionsBasic(t *testing.T) {
	res := ExtractOutputRedirections("echo hi > /tmp/x")
	if len(res.Redirections) != 1 ||
		res.Redirections[0].Target != "/tmp/x" || res.Redirections[0].Operator != ">" {
		t.Errorf("应提取 > /tmp/x：%+v", res.Redirections)
	}
	if res.HasDangerousRedirection {
		t.Errorf("静态目标不应标记危险")
	}
	if strings.Contains(res.CommandWithoutRedirections, ">") {
		t.Errorf("重建命令不应含重定向：%q", res.CommandWithoutRedirections)
	}
	if !strings.Contains(res.CommandWithoutRedirections, "echo hi") {
		t.Errorf("重建命令应保留原命令体：%q", res.CommandWithoutRedirections)
	}

	res = ExtractOutputRedirections("echo hi >> /tmp/x")
	if len(res.Redirections) != 1 || res.Redirections[0].Operator != ">>" {
		t.Errorf("应提取 >> 形态：%+v", res.Redirections)
	}
}

// TestExtractRedirectionsDangerous 危险展开 fail 标记（hasDangerous
// Expansion 路径——$ 与 tilde 族为双不命中间隙封堵场景）。分层说明：
// % 形态由 ValidatePath 的 shell 展开拒收层处理（isSimpleTarget 不拒
// %，捕获后校验拒）；!hist 走 >!filename 捕获路径（剥 ! 后校验）；
// >(...) 进程替换由 checkPathConstraints 层兜底——三者均不在本层
// dangerous 标记范围。
func TestExtractRedirectionsDangerous(t *testing.T) {
	for _, cmd := range []string{
		"echo hi > $HOME/x",
		"echo hi > ~/.bashrc",
		"echo hi > =cmd",
		"echo hi > *.txt",
	} {
		res := ExtractOutputRedirections(cmd)
		if !res.HasDangerousRedirection {
			t.Errorf("%q 应标记危险展开（双不命中间隙封堵）", cmd)
		}
	}
}

// TestExtractRedirectionsParseFailClosed 解析失败 fail-closed（${}
// 畸形——静默跳过即绕过，上游 688-698）。
func TestExtractRedirectionsParseFailClosed(t *testing.T) {
	res := ExtractOutputRedirections(`echo ${}`)
	if !res.HasDangerousRedirection {
		t.Errorf("解析失败应 fail-closed 标记危险")
	}
	if res.CommandWithoutRedirections != `echo ${}` {
		t.Errorf("fail-closed 应原样返回命令：%q", res.CommandWithoutRedirections)
	}
}

// TestExtractRedirectionsFD FD 形态：2>file 提取、2>&1 复制剥离、
// 非 stdout 保留命令体。
func TestExtractRedirectionsFD(t *testing.T) {
	res := ExtractOutputRedirections("cmd 2>/tmp/err")
	if len(res.Redirections) != 1 || res.Redirections[0].Target != "/tmp/err" {
		t.Errorf("2>/tmp/err 应提取 err 目标：%+v", res.Redirections)
	}

	res = ExtractOutputRedirections("cmd > out 2>&1")
	if len(res.Redirections) != 1 || res.Redirections[0].Target != "out" {
		t.Errorf("2>&1 复制不应产生重定向条目：%+v", res.Redirections)
	}
	if !strings.Contains(res.CommandWithoutRedirections, "2>&1") {
		t.Errorf("2>&1 应保留在重建命令中：%q", res.CommandWithoutRedirections)
	}
}

// TestExtractRedirectionsHeredocBody heredoc 体内的 > 不算重定向
// （体被占位符替代——上游 642-667 攻击样例的反面验证）。
func TestExtractRedirectionsHeredocBody(t *testing.T) {
	res := ExtractOutputRedirections("cat <<'EOF'\n> /etc/passwd\nEOF")
	if len(res.Redirections) != 0 {
		t.Errorf("heredoc 体内 > 不应提取为重定向：%+v", res.Redirections)
	}
	if res.HasDangerousRedirection {
		t.Errorf("heredoc 体不应标记危险")
	}
}

// TestExtractRedirectionsQuotedHeredocAttack 上游 653-660 攻击样例：
// cat <<'ls' + x\ + ls 闭合 + > /etc/passwd——提取顺序错误会吞目标。
func TestExtractRedirectionsQuotedHeredocAttack(t *testing.T) {
	cmd := "cat <<'ls'\nx\\\nls\n> /etc/passwd\nls"
	res := ExtractOutputRedirections(cmd)
	if len(res.Redirections) != 1 || res.Redirections[0].Target != "/etc/passwd" {
		t.Errorf("heredoc 闭合后的重定向必须被捕获：%+v", res.Redirections)
	}
}

// TestExtractRedirectionsCmdSubstitution 命令替换内重定向排除。
func TestExtractRedirectionsCmdSubstitution(t *testing.T) {
	res := ExtractOutputRedirections("echo $(cat > /etc/passwd)")
	if len(res.Redirections) != 0 {
		t.Errorf("命令替换内重定向不应在外层提取：%+v", res.Redirections)
	}
}

// TestExtractRedirectionsSubshell 重定向子 shell：括号剥离 + 目标提取。
func TestExtractRedirectionsSubshell(t *testing.T) {
	res := ExtractOutputRedirections("(echo a) > /tmp/x")
	if len(res.Redirections) != 1 || res.Redirections[0].Target != "/tmp/x" {
		t.Errorf("子 shell 重定向应提取目标：%+v", res.Redirections)
	}
	if strings.Contains(res.CommandWithoutRedirections, "(") {
		t.Errorf("被重定向子 shell 的括号应剥离（上游 739-745）：%q",
			res.CommandWithoutRedirections)
	}
	if !strings.Contains(res.CommandWithoutRedirections, "echo a") {
		t.Errorf("重建应保留子 shell 命令体：%q", res.CommandWithoutRedirections)
	}
}

// TestExtractRedirectionsZshForce zsh 强覆盖形态：>! 与 >| 目标剥前缀
// 捕获。
func TestExtractRedirectionsZshForce(t *testing.T) {
	res := ExtractOutputRedirections("echo x >! /tmp/y")
	if len(res.Redirections) != 1 || res.Redirections[0].Target != "/tmp/y" {
		t.Errorf(">! 目标应剥 ! 捕获：%+v", res.Redirections)
	}
	res = ExtractOutputRedirections("echo x >| /tmp/y")
	if len(res.Redirections) != 1 || res.Redirections[0].Target != "/tmp/y" {
		t.Errorf(">| 目标应剥 | 捕获：%+v", res.Redirections)
	}
}

// TestExtractRedirectionsMulti 多重定向按序提取。
func TestExtractRedirectionsMulti(t *testing.T) {
	res := ExtractOutputRedirections("echo a > /tmp/one && echo b >> /tmp/two")
	want := []OutputRedirection{
		{Target: "/tmp/one", Operator: ">"},
		{Target: "/tmp/two", Operator: ">>"},
	}
	if !reflect.DeepEqual(res.Redirections, want) {
		t.Errorf("多重定向应按序提取：%+v", res.Redirections)
	}
}

// TestIsSimpleTargetAndDangerous 判定语义（对齐上游 isSimpleTarget
// 798-817 与 hasDangerousExpansion 830-858 的字符集边界）。两判定为
// OR 关系非互斥（上游 826-828 不变量）：%VAR% 同时命中两者（简单+
// 危险），handleRedirection 中 simple 分支先消费——捕获后由
// ValidatePath 的 shell 展开拒收层处理。
func TestIsSimpleTargetAndDangerous(t *testing.T) {
	simpleOnly := []string{"/tmp/x", "out.txt", "a-b_c.txt"}
	for _, s := range simpleOnly {
		tok := ParseToken{Kind: TokenKindStr, Str: s}
		if !sqIsSimpleTarget(tok) {
			t.Errorf("%q 应为简单目标", s)
		}
		if sqHasDangerousExpansion(tok) {
			t.Errorf("%q 不应标记危险", s)
		}
	}
	// 双真形态：simple 优先消费（捕获 + ValidatePath 拒收）
	for _, s := range []string{"%VAR%"} {
		tok := ParseToken{Kind: TokenKindStr, Str: s}
		if !sqIsSimpleTarget(tok) {
			t.Errorf("%q 应为简单目标（百分号不在拒绝集）", s)
		}
		if !sqHasDangerousExpansion(tok) {
			t.Errorf("%q 应标记危险（含百分号）", s)
		}
	}
	dangerousOnly := []string{"$VAR", "`cmd`", "*.txt", "a?b", "[ab]", "{a,b}", "!h", "=c", "~", "~/x"}
	for _, s := range dangerousOnly {
		tok := ParseToken{Kind: TokenKindStr, Str: s}
		if sqIsSimpleTarget(tok) {
			t.Errorf("%q 不应为简单目标", s)
		}
		if !sqHasDangerousExpansion(tok) {
			t.Errorf("%q 应标记危险", s)
		}
	}
}
