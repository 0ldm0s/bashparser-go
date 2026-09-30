// heredoc_match_test.go——守卫语义版起始匹配/闭合定位行为测试
// （用例转译自上游 programWriteGuard.ts 66-78 注释清单）。

package parser

import "testing"

// TestMatchHeredocStartGuard 守卫语义起始匹配。
func TestMatchHeredocStartGuard(t *testing.T) {
	cases := []struct {
		cmd     string
		wantOK  bool
		wantDel string
	}{
		{"cat <<EOF > b\nbody\nEOF", true, "EOF"},
		{`cat >> b << 'PROCEOF'` + "\nbody\nPROCEOF", true, "PROCEOF"},
		{"cat <<-DELIM\n\tbody\nDELIM", true, "DELIM"},
		// 守卫正则无反斜杠分支（上游 HEREDOC_START_RE 仅有引号配对/裸
		// \w 两分支）、且要求起始行后有换行——两形态均不命中
		{"cat <<\\EOF\nbody\nEOF", false, ""},
		{"echo a << b", false, ""},
		{"echo a << b\nx", true, "b"}, // 换行存在即命中
		{"cat <<<x", false, ""},       // <<<：<< 后 \w 为空且 '-' 不符
		{"cat <<'EO F'\nbody\nEO F", false, ""}, // 配对引号内 \w 提前断
		{"echo plain", false, ""},               // 无 <<（快进路径）
	}
	for _, tc := range cases {
		_, _, delim, ok := MatchHeredocStart(tc.cmd, 0)
		if ok != tc.wantOK {
			t.Errorf("MatchHeredocStart(%q) ok=%v, 期望 %v", tc.cmd, ok, tc.wantOK)
			continue
		}
		if ok && delim != tc.wantDel {
			t.Errorf("MatchHeredocStart(%q) delim=%q, 期望 %q", tc.cmd, delim, tc.wantDel)
		}
	}
}

// TestFindHeredocCloseLineGuard 闭合定位：剥体终点为闭合行尾。
func TestFindHeredocCloseLineGuard(t *testing.T) {
	cmd := "cat <<EOF\nbody\nEOF\ntail"
	_, bodyStart, delim, ok := MatchHeredocStart(cmd, 0)
	if !ok {
		t.Fatal("应匹配 heredoc 起始")
	}
	end := FindHeredocCloseLine(cmd, bodyStart, delim)
	if end < 0 {
		t.Fatal("应找到闭合行")
	}
	// 剥体验证：起始行保留 + 闭合行移除后 tail 紧跟
	stripped := cmd[:bodyStart] + cmd[end:]
	if stripped != "cat <<EOF\ntail" {
		t.Errorf("剥体结果=%q, 期望 %q", stripped, "cat <<EOF\ntail")
	}

	// <<- 剥前导 tab 闭合
	cmd2 := "cat <<-EOF\n\t\tbody\n\tEOF\ntail2"
	_, bodyStart2, delim2, ok2 := MatchHeredocStart(cmd2, 0)
	if !ok2 {
		t.Fatal("<<- 应匹配")
	}
	end2 := FindHeredocCloseLine(cmd2, bodyStart2, delim2)
	if end2 < 0 {
		t.Fatal("应找到 <<- 闭合行")
	}
	stripped2 := cmd2[:bodyStart2] + cmd2[end2:]
	if stripped2 != "cat <<-EOF\ntail2" {
		t.Errorf("<<- 剥体结果=%q", stripped2)
	}

	// 闭合行尾允许 [\s&|;`)]（EOF; 形态——尾全为允许字符方为闭合；
	// "EOF; && x" 尾含字母非闭合，上游 closeRe 同样拒绝）
	cmd3 := "cat <<EOF\nbody\nEOF;"
	_, bodyStart3, delim3, ok3 := MatchHeredocStart(cmd3, 0)
	if !ok3 {
		t.Fatal("cmd3 应匹配起始")
	}
	end3 := FindHeredocCloseLine(cmd3, bodyStart3, delim3)
	if end3 < 0 {
		t.Errorf("闭合行尾 EOF; 应命中")
	}
	if got := cmd3[:bodyStart3] + cmd3[end3:]; got != "cat <<EOF\n" {
		t.Errorf("剥体结果=%q", got)
	}
	// 尾含普通字符非闭合
	cmd4 := "cat <<EOF\nbody\nEOF; && x"
	if got := FindHeredocCloseLine(cmd4, 11, "EOF"); got != -1 {
		t.Errorf("EOF; && x 非闭合行应返回 -1，得到 %d", got)
	}

	// 无闭合 → -1
	if got := FindHeredocCloseLine("cat <<EOF\nno closing", 11, "EOF"); got != -1 {
		t.Errorf("无闭合应返回 -1，得到 %d", got)
	}
}
