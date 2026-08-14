package app

import (
	"context"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 多个来源合并：URL 列表 + 粘贴文本，按 ip:端口 去重；坏 URL 跳过并记警告
func TestBuildCandidateSourcesMergesAndDedupes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.txt":
			_, _ = w.Write([]byte("104.20.1.1:443\n104.20.1.2\n"))
		case "/b.txt":
			_, _ = w.Write([]byte("104.20.1.1:443\n# 重复行\n104.20.1.3:2053\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	src := ScheduleSources{
		URLs: []string{srv.URL + "/a.txt", srv.URL + "/b.txt", srv.URL + "/missing"},
		Text: "104.20.1.3:2053\n8.8.8.8\n",
	}
	items, stats, err := BuildCandidateSources(context.Background(), src, 443)
	if err != nil {
		t.Fatalf("整体应成功: %v", err)
	}
	// 去重后应是 4 条：104.20.1.1:443 / 104.20.1.2:443 / 104.20.1.3:2053 / 8.8.8.8:443
	if len(items) != 4 {
		t.Fatalf("want 4, got %d: %v", len(items), items)
	}
	warned := false
	for _, st := range stats {
		if st.Source == "url" && st.Name == srv.URL+"/missing" && st.Warning == "" {
			t.Error("404 的 URL 应带警告")
		}
		if st.Warning != "" {
			warned = true
		}
	}
	if !warned {
		t.Error("失败的来源应记警告")
	}
	// 无端口条目应补默认端口
	for _, it := range items {
		if it.Port <= 0 {
			t.Errorf("端口应补齐: %+v", it)
		}
	}
}

// 来源全空应报错而不是静默跑空列表
func TestBuildCandidateSourcesEmpty(t *testing.T) {
	_, _, err := BuildCandidateSources(context.Background(), ScheduleSources{}, 443)
	if err == nil {
		t.Fatal("空来源应报错")
	}
}

// URL 超时要能及时失败，不拖垮整轮
func TestFetchProxyListTimeout(t *testing.T) {
	old := urlFetchTimeout
	urlFetchTimeout = 50 * time.Millisecond
	defer func() { urlFetchTimeout = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("1.1.1.1\n"))
	}))
	defer srv.Close()

	if _, err := fetchProxyList(context.Background(), srv.URL); err == nil {
		t.Fatal("超时应返回错误")
	}
}

// 随机 CF IP 生成：数量正确、都落在官方网段内、端口补齐、无重复
func TestRandomIPsInRanges(t *testing.T) {
	parse := func(s string) *net.IPNet {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	ranges := []*net.IPNet{
		parse("10.0.0.0/30"),    // 4 个地址
		parse("192.168.1.0/24"), // 256 个地址
		parse("172.16.0.0/32"),  // 1 个地址
	}
	rng := rand.New(rand.NewSource(42))
	got := randomIPsInRanges(ranges, 50, 2053, rng)
	if len(got) != 50 {
		t.Fatalf("want 50, got %d", len(got))
	}
	seen := map[string]bool{}
	for _, r := range got {
		if r.Port != 2053 {
			t.Errorf("端口应补 2053: %+v", r)
		}
		ip := net.ParseIP(r.IP)
		if ip == nil {
			t.Fatalf("非法 IP: %q", r.IP)
		}
		in := false
		for _, n := range ranges {
			if n.Contains(ip) {
				in = true
				break
			}
		}
		if !in {
			t.Errorf("%s 不在任何网段内", r.IP)
		}
		if seen[r.IP] {
			t.Errorf("IP 重复: %s", r.IP)
		}
		seen[r.IP] = true
	}
}

// 随机生成数量超过网段容量时，最多给出全部去重后的地址
func TestRandomIPsInRangesCapsAtCapacity(t *testing.T) {
	_, n, err := net.ParseCIDR("10.0.0.0/30")
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(1))
	got := randomIPsInRanges([]*net.IPNet{n}, 100, 443, rng)
	if len(got) != 4 {
		t.Fatalf("/30 网段最多 4 个地址，got %d", len(got))
	}
}

// 日志追加要限长，只留末尾
func TestAppendLogTruncates(t *testing.T) {
	big := ""
	for i := 0; i < 500; i++ {
		big += "x"
	}
	out := appendLog("", []string{big, "tail-line"})
	if len(out) > 4000 {
		t.Fatalf("日志超长: %d", len(out))
	}
	if out[len(out)-len("tail-line"):] != "tail-line" {
		t.Fatalf("应保留末尾行, got %q", out[len(out)-30:])
	}
}
