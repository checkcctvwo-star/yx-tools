package app

import "testing"

// 无配额：走旧逻辑，按 Limit 截断，固定条目追加在最后
func TestBuildUploadListNoQuota(t *testing.T) {
	rs := makeColoResults(10, []string{"HKG", "SIN"})
	out, warns := BuildUploadList(rs, UploadPlan{Limit: 3})
	if len(out) != 3 {
		t.Fatalf("无配额应截断到 3，got %d", len(out))
	}
	if len(warns) != 0 {
		t.Fatalf("不应有警告: %v", warns)
	}
}

// 配额：各地区按数量取自己的结果
func TestBuildUploadListQuotas(t *testing.T) {
	// 10 条：HKG×5, SIN×5（前 5 个 HKG，后 5 个 SIN）
	rs := append(makeColoResults(5, []string{"HKG"}), makeColoResults(5, []string{"SIN"})...)
	plan := UploadPlan{
		Quotas: []RegionQuota{
			{Name: "香港", Colos: []string{"HKG"}, Count: 3},
			{Name: "新加坡", Colos: []string{"SIN"}, Count: 2},
		},
	}
	out, warns := BuildUploadList(rs, plan)
	if len(out) != 5 {
		t.Fatalf("应取 3+2=5 条，got %d", len(out))
	}
	hkg, sin := 0, 0
	for _, r := range out {
		switch r.Colo {
		case "HKG":
			hkg++
		case "SIN":
			sin++
		}
	}
	if hkg != 3 || sin != 2 {
		t.Fatalf("配额分配错误: HKG=%d SIN=%d", hkg, sin)
	}
	if len(warns) != 0 {
		t.Fatalf("足量配额不应有警告: %v", warns)
	}
}

// 不足就少传（默认）：配额多于实际时警告并少传
func TestBuildUploadListShortfallNoFill(t *testing.T) {
	rs := makeColoResults(2, []string{"HKG"})
	plan := UploadPlan{Quotas: []RegionQuota{{Name: "香港", Colos: []string{"HKG"}, Count: 5}}}
	out, warns := BuildUploadList(rs, plan)
	if len(out) != 2 {
		t.Fatalf("不足就少传：应传 2 条，got %d", len(out))
	}
	if len(warns) == 0 {
		t.Fatal("不足时应给警告")
	}
}

// 用其他地区补位：缺口从剩余结果（含其他地区、含无地区码）填满
func TestBuildUploadListFill(t *testing.T) {
	// HKG×2 + 无地区码×2
	rs := append(makeColoResults(2, []string{"HKG"}), makeColoResults(2, []string{""})...)
	plan := UploadPlan{
		Quotas:    []RegionQuota{{Name: "香港", Colos: []string{"HKG"}, Count: 4}},
		QuotaFill: true,
	}
	out, warns := BuildUploadList(rs, plan)
	if len(out) != 4 {
		t.Fatalf("补位应凑满 4 条，got %d", len(out))
	}
	_ = warns
}

// 「其他地区」行：从剩余（非配额地区）结果按速度取前 N，不足再全量补
func TestBuildUploadListOtherQuota(t *testing.T) {
	// HKG×2 + LAX×3
	rs := append(makeColoResults(2, []string{"HKG"}), makeColoResults(3, []string{"LAX"})...)
	plan := UploadPlan{
		Quotas:     []RegionQuota{{Name: "香港", Colos: []string{"HKG"}, Count: 2}},
		OtherQuota: 2,
	}
	out, _ := BuildUploadList(rs, plan)
	if len(out) != 4 {
		t.Fatalf("应取 2(香港)+2(其他)=4 条，got %d", len(out))
	}
}

// 固定附带条目：去重（同名结果被删）、带名字、排在最后
func TestBuildUploadListFixed(t *testing.T) {
	rs := []Result{
		{IP: "1.1.1.1", Port: 443, Colo: "HKG"},
		{IP: "2.2.2.2", Port: 443, Colo: "SIN"},
	}
	plan := UploadPlan{
		Fixed: []FixedItem{
			{Addr: "1.1.1.1", Name: "家宽"}, // 与结果重复 → 保留固定条目
			{Addr: "cdn.example.com", Name: "机场CNAME"},
			{Addr: "bad addr/with", Name: "解析不了"}, // 应被跳过
		},
	}
	out, warns := BuildUploadList(rs, plan)
	// 结果 2.2.2.2 + 固定 1.1.1.1 + cdn.example.com = 3 条
	if len(out) != 3 {
		t.Fatalf("应 3 条，got %d: %+v", len(out), out)
	}
	if out[0].IP != "2.2.2.2" || out[0].Fixed {
		t.Fatalf("测速结果应在前面: %+v", out[0])
	}
	if out[1].IP != "1.1.1.1" || !out[1].Fixed || out[1].ColoName != "家宽" {
		t.Fatalf("重复 IP 应保留固定条目: %+v", out[1])
	}
	if out[2].IP != "cdn.example.com" || out[2].Port != 443 || out[2].ColoName != "机场CNAME" {
		t.Fatalf("域名条目默认 443 且带名字: %+v", out[2])
	}
	if len(warns) == 0 {
		t.Fatal("解析不了的固定条目应给警告")
	}
}

// parseFixedAddr 的各种输入
func TestParseFixedAddr(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
		ok   bool
	}{
		{"1.2.3.4", "1.2.3.4", 443, true},
		{"1.2.3.4:2053", "1.2.3.4", 2053, true},
		{"cdn.example.com", "cdn.example.com", 443, true},
		{"cdn.example.com:8443", "cdn.example.com", 8443, true},
		{"[::1]:443", "::1", 443, true},
		{"", "", 0, false},
		{"bad addr", "", 0, false},
		{"a/b", "", 0, false},
		{"1.2.3.4:99999", "", 0, false},
	}
	for _, c := range cases {
		host, port, ok := parseFixedAddr(c.in)
		if ok != c.ok || host != c.host || port != c.port {
			t.Errorf("parseFixedAddr(%q) = (%q,%d,%v), want (%q,%d,%v)",
				c.in, host, port, ok, c.host, c.port, c.ok)
		}
	}
}

// nodeName 对固定条目返回用户名字，对测速结果返回 地区-速度
func TestNodeNameFixed(t *testing.T) {
	if got := nodeName(Result{IP: "x", Fixed: true, ColoName: "我的家宽"}); got != "我的家宽" {
		t.Fatalf("固定条目应返回用户名字: %q", got)
	}
	if got := nodeName(Result{IP: "1.1.1.1", Colo: "HKG", Speed: 8.5}); got != "香港-8.50MB/s" {
		t.Fatalf("测速结果应带速度: %q", got)
	}
}
