// heredoc_extract_test.go——heredoc 文本级提取/还原行为测试（用例源
// 自上游 heredoc.ts 安全注释中的走私样例与 bash 语义要点）。

package parser

import (
	"strings"
	"testing"
)

// TestExtractHeredocsBasic 基本提取与还原往返。
func TestExtractHeredocsBasic(t *testing.T) {
	cmd := "cat <<EOF\nhello world\nEOF"
	res := ExtractHeredocs(cmd, false)
	if len(res.Heredocs) != 1 {
		t.Fatalf("应提取 1 个 heredoc，得到 %d", len(res.Heredocs))
	}
	if !strings.Contains(res.ProcessedCommand, "__HEREDOC_0_") {
		t.Errorf("处理命令应含占位符：%q", res.ProcessedCommand)
	}
	if strings.Contains(res.ProcessedCommand, "hello world") {
		t.Errorf("体内容应被提取移除：%q", res.ProcessedCommand)
	}
	// 占位符替换保同行内容：cat 前缀保留
	if !strings.HasPrefix(res.ProcessedCommand, "cat ") {
		t.Errorf("命令前缀应保留：%q", res.ProcessedCommand)
	}
	// 还原往返
	var placeholder string
	for p := range res.Heredocs {
		placeholder = p
	}
	if res.Heredocs[placeholder].FullText != "<<EOF\nhello world\nEOF" {
		t.Errorf("FullText 应含操作符+体：%q", res.Heredocs[placeholder].FullText)
	}
	restored := RestoreHeredocs([]string{res.ProcessedCommand}, res.Heredocs)
	if restored[0] != "<<EOF\nhello world\nEOF" && !strings.Contains(restored[0], "<<EOF") {
		t.Errorf("还原后应含操作符+体：%q", restored[0])
	}
}

// TestExtractHeredocsSameLine 同行后续内容保留（cat <<EOF && echo done
// 形态——&& echo done 是命令非体）。
func TestExtractHeredocsSameLine(t *testing.T) {
	cmd := "cat <<EOF && echo done\nbody\nEOF"
	res := ExtractHeredocs(cmd, false)
	if len(res.Heredocs) != 1 {
		t.Fatalf("应提取 1 个 heredoc，得到 %d", len(res.Heredocs))
	}
	if !strings.Contains(res.ProcessedCommand, "&& echo done") {
		t.Errorf("同行内容应保留：%q", res.ProcessedCommand)
	}
}

// TestExtractHeredocsQuotedInner 引号内 << 非 heredoc 操作符。
func TestExtractHeredocsQuotedInner(t *testing.T) {
	res := ExtractHeredocs(`echo "<<EOF"`, false)
	if len(res.Heredocs) != 0 {
		t.Errorf("引号内 << 不应提取：%+v", res.Heredocs)
	}
}

// TestExtractHeredocsDollarQuoteBail $'...' / $"..." 整体放弃提取。
func TestExtractHeredocsDollarQuoteBail(t *testing.T) {
	res := ExtractHeredocs("echo $'x' <<EOF\nbody\nEOF", false)
	if len(res.Heredocs) != 0 || res.ProcessedCommand != "echo $'x' <<EOF\nbody\nEOF" {
		t.Errorf("含 $' 形态应整体放弃提取")
	}
}

// TestExtractHeredocsBacktickBail 首 << 前反引号整体放弃。
func TestExtractHeredocsBacktickBail(t *testing.T) {
	res := ExtractHeredocs("echo `x` <<EOF\nbody\nEOF", false)
	if len(res.Heredocs) != 0 {
		t.Errorf("首 << 前反引号应整体放弃提取")
	}
}

// TestExtractHeredocsArithBail 未闭合 (( 算术上下文整体放弃
// （(1 << 2) 的 << 是位移非 heredoc）。
func TestExtractHeredocsArithBail(t *testing.T) {
	res := ExtractHeredocs("(( x = 1 << 2 ))\necho done", false)
	if len(res.Heredocs) != 0 {
		t.Errorf("未闭合算术上下文应整体放弃提取")
	}
}

// TestExtractHeredocsNoClosing 无闭合定界符跳过。
func TestExtractHeredocsNoClosing(t *testing.T) {
	res := ExtractHeredocs("cat <<EOF\nno closing", false)
	if len(res.Heredocs) != 0 {
		t.Errorf("无闭合定界符不应提取")
	}
}

// TestExtractHeredocsDashStripTabs <<- 剥前导 tab。
func TestExtractHeredocsDashStripTabs(t *testing.T) {
	cmd := "cat <<-\tEOF\n\t\tbody\n\tEOF"
	res := ExtractHeredocs(cmd, false)
	if len(res.Heredocs) != 1 {
		t.Fatalf("<<- 应剥 tab 提取，得到 %d", len(res.Heredocs))
	}
	for _, info := range res.Heredocs {
		if !strings.Contains(info.FullText, "body") {
			t.Errorf("体应含 body（含前导 tab 行）：%q", info.FullText)
		}
	}
}

// TestExtractHeredocsQuotedOnly quotedOnly 模式：未引号 heredoc 跳过，
// 其体内嵌套的引号 heredoc 拒绝提取（防 $(evil) 藏进占位符——上游
// 174-183 样例）。
func TestExtractHeredocsQuotedOnly(t *testing.T) {
	cmd := "cat <<EOF\n<<'SAFE'\n$(evil)\nSAFE\nEOF"
	res := ExtractHeredocs(cmd, true)
	if len(res.Heredocs) != 0 {
		t.Errorf("quotedOnly 下嵌套引号 heredoc 应被拒绝提取：%+v", res.Heredocs)
	}

	// quotedOnly 下独立引号 heredoc 正常提取
	res = ExtractHeredocs("cat <<'EOF'\nliteral body\nEOF", true)
	if len(res.Heredocs) != 1 {
		t.Errorf("quotedOnly 下引号 heredoc 应提取，得到 %d", len(res.Heredocs))
	}

	// quotedOnly 下未引号 heredoc 跳过
	res = ExtractHeredocs("cat <<EOF\nbody\nEOF", true)
	if len(res.Heredocs) != 0 {
		t.Errorf("quotedOnly 下未引号 heredoc 应跳过")
	}
}

// TestExtractHeredocsEofTokenEarlyClose PST_EOFTOKEN 早闭形态放弃
// （定界符行首 + 后继 `)}|&;(<> 字符——make_cmd.c:606 语义）。
func TestExtractHeredocsEofTokenEarlyClose(t *testing.T) {
	// EOF 后跟 ` ——bash 可能在此早闭 heredoc，按精确匹配会错位
	cmd := "cat <<EOF\nEOF`\nevil\nEOF"
	res := ExtractHeredocs(cmd, false)
	if len(res.Heredocs) != 0 {
		t.Errorf("PST_EOFTOKEN 早闭形态应放弃提取")
	}
}

// TestExtractHeredocsLineContinuation 同行内容以奇数反斜杠收尾（行
// 连续）放弃提取（上游 442-471 样例：cat <<'EOF' && \\ 形态）。
func TestExtractHeredocsLineContinuation(t *testing.T) {
	cmd := "cat <<'EOF' && \\\nrm -rf /\ncontent\nEOF"
	res := ExtractHeredocs(cmd, false)
	if len(res.Heredocs) != 0 {
		t.Errorf("行连续形态应放弃提取")
	}
}

// TestExtractHeredocsDelimiterWordExtend 定界符词延伸校验（<<'EOF'a
// 的 bash 定界符是 EOFa——捕获 EOF 应跳过）。
func TestExtractHeredocsDelimiterWordExtend(t *testing.T) {
	cmd := "cat <<'EOF'a\nbody\nEOFa"
	res := ExtractHeredocs(cmd, false)
	if len(res.Heredocs) != 0 {
		t.Errorf("定界符词延伸形态应跳过（bash 定界符为 EOFa）")
	}
}

// TestContainsHeredoc 快检（对齐上游正则语义：<< 后 [ \t]* + \w+
// 即命中——`<< b` 形态检出为真；<<< 与单 < 不命中）。
func TestContainsHeredoc(t *testing.T) {
	if !ContainsHeredoc("cat <<EOF\nx\nEOF") {
		t.Errorf("含 heredoc 应检出")
	}
	if !ContainsHeredoc("echo a << b") {
		t.Errorf("<< 空格 + \\w 定界符形态应检出（对齐上游正则）")
	}
	if ContainsHeredoc("echo a <<<b") {
		t.Errorf("<<<（here-string）不应检出")
	}
	if ContainsHeredoc("echo a<b") {
		t.Errorf("单 < 不应检出")
	}
	if ContainsHeredoc("echo plain") {
		t.Errorf("无 heredoc 不应检出")
	}
}
