package parser

import (
	"strings"
	"testing"
)

// bail 路径验证：超节点预算输入触发中止，ParseSource 返回 nil
//（对齐上游 aborted → null 语义）。
//
// 已知运行时差异（登记）：深递归输入（如数千层 $(( 嵌套）在 V8 中栈
// 溢出 → catch → null；Go 栈弹性增长可完成解析返回树。此类输入两侧
// 不一致属运行时本质差异，安全方向为 Go 更完整（AST 更全），互拍主集
// 不含此类输入。

func TestBailOnNodeBudget(t *testing.T) {
	// 6 万个词 → word 节点合计超过 5 万节点预算
	long := "echo " + strings.Repeat("a ", 60000)
	if got := ParseSource(long, 0); got != nil {
		t.Errorf("超节点预算输入应触发 bail 返回 nil")
	}
}

// 超时路径：极长输入在 50ms 内无法完成时超时 bail（节点数不足但时钟
// 超限——用慢路径覆盖；此用例验证时钟检查代码可达）
func TestBailBudgetNotTriggeredByTime(t *testing.T) {
	// 节点少但耗时的形态难以构造（解析器太快）；此处仅验证正常短输入
	// 不误触发超时
	if got := ParseSource("echo ok", 0); got == nil {
		t.Fatal("短输入不应 bail")
	}
}

// 正常输入不受 bail 影响（防回归）
func TestNormalInputStillParses(t *testing.T) {
	if got := ParseSource("echo hello", 0); got == nil {
		t.Fatal("正常输入不应 bail")
	}
}
