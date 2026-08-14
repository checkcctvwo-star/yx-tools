package app

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// urlFetchTimeout 单个来源 URL 的抓取超时；失败的来源跳过并记警告，
// 不让一个坏链接拖垮整轮任务。
var urlFetchTimeout = 15 * time.Second

// urlBodyLimit 单个 URL 的响应体上限，防止异常大的响应拖垮内存
const urlBodyLimit = 2 << 20 // 2MB

// SourceStat 描述一个候选来源的加载结果，供界面预览与日志展示
type SourceStat struct {
	Source  string `json:"source"` // url / text / random_cf
	Name    string `json:"name"`   // 展示用名称：URL 或描述
	Count   int    `json:"count"`  // 该来源解析出的条数
	Warning string `json:"warning,omitempty"`
}

// BuildCandidateSources 抓取 URL、解析文本、随机生成 CF IP，合并去重。
// port 用于补齐没有端口的条目（通常取任务快照的端口）。
func BuildCandidateSources(ctx context.Context, s ScheduleSources, port int) ([]Result, []SourceStat, error) {
	if port <= 0 {
		port = 443
	}
	var stats []SourceStat
	var warnings []string
	seen := make(map[string]struct{})
	var items []Result
	add := func(r Result) bool {
		if r.Port <= 0 {
			r.Port = port
		}
		key := net.JoinHostPort(r.IP, strconv.Itoa(r.Port))
		if _, dup := seen[key]; dup {
			return false
		}
		seen[key] = struct{}{}
		items = append(items, r)
		return true
	}

	// 1. URL 来源：每次执行前重新抓取，失败跳过
	for _, raw := range s.URLs {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		st := SourceStat{Source: "url", Name: u}
		rs, err := fetchProxyList(ctx, u)
		if err != nil {
			st.Warning = err.Error()
			warnings = append(warnings, u+" 加载失败: "+err.Error())
		} else {
			for _, r := range rs {
				add(r)
			}
			st.Count = len(rs)
		}
		stats = append(stats, st)
	}

	// 2. 粘贴文本
	if text := strings.TrimSpace(s.Text); text != "" {
		rs, err := ParseProxySource(text)
		st := SourceStat{Source: "text", Name: "粘贴文本"}
		if err != nil {
			st.Warning = err.Error()
			warnings = append(warnings, "粘贴文本解析失败: "+err.Error())
		} else {
			for _, r := range rs {
				add(r)
			}
			st.Count = len(rs)
		}
		stats = append(stats, st)
	}

	// 3. 随机 Cloudflare IPv4
	if s.RandomCF {
		count := s.RandomCFCount
		if count <= 0 {
			count = 100
		}
		st := SourceStat{Source: "random_cf", Name: "随机 Cloudflare IPv4"}
		rs, err := RandomCFIPs(ctx, count, port)
		if err != nil {
			st.Warning = err.Error()
			warnings = append(warnings, "随机 CF IP 生成失败: "+err.Error())
		} else {
			for _, r := range rs {
				add(r)
			}
			st.Count = len(rs)
		}
		stats = append(stats, st)
	}

	if len(items) == 0 {
		if len(warnings) > 0 {
			return nil, stats, fmt.Errorf("没有可用的候选 IP：%s", strings.Join(warnings, "；"))
		}
		return nil, stats, fmt.Errorf("没有可用的候选 IP，请填写 URL 列表或粘贴 IP")
	}
	return items, stats, nil
}

// fetchProxyList 抓取一个 URL 并按 IP/IP:端口 逐行解析
func fetchProxyList(ctx context.Context, u string) ([]Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	fctx, cancel := context.WithTimeout(ctx, urlFetchTimeout)
	defer cancel()
	req = req.WithContext(fctx)
	// 伪装一下浏览器 UA，个别站点对默认 Go UA 返回 403
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 yx-tools")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, urlBodyLimit))
	if err != nil {
		return nil, err
	}
	rs, err := ParseProxySource(string(body))
	if err != nil {
		return nil, err
	}
	return rs, nil
}

// CFIPv4URLs 是 Cloudflare 官方 IPv4 网段列表，随机 IP 从这里生成。
// 与测速默认来源保持一致：文件下载有缓存，见 ensureIPFile。
func loadCFRanges(ctx context.Context) ([]*net.IPNet, error) {
	path, err := ensureIPFile(ctx, false)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ranges []*net.IPNet
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, n, err := net.ParseCIDR(line); err == nil {
			ranges = append(ranges, n)
			continue
		}
		// 兼容裸 IP 行：当作 /32
		if ip := net.ParseIP(line); ip != nil {
			ranges = append(ranges, &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)})
		}
	}
	if len(ranges) == 0 {
		return nil, fmt.Errorf("官方 IP 段文件里没有可用网段")
	}
	return ranges, nil
}

// RandomCFIPs 从 Cloudflare 官方 IPv4 网段随机生成 count 个 IP。
// 按网段大小加权，保证大网段（如 104.16.0.0/12）出现的概率与容量成正比。
func RandomCFIPs(ctx context.Context, count int, port int) ([]Result, error) {
	if count <= 0 {
		count = 100
	}
	if port <= 0 {
		port = 443
	}
	ranges, err := loadCFRanges(ctx)
	if err != nil {
		return nil, err
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return randomIPsInRanges(ranges, count, port, rng), nil
}

// randomIPsInRanges 是 RandomCFIPs 的纯函数内核，注入 rng 便于测试
func randomIPsInRanges(ranges []*net.IPNet, count, port int, rng *rand.Rand) []Result {
	var total uint64
	for _, n := range ranges {
		ones, bits := n.Mask.Size()
		total += uint64(1) << (bits - ones)
	}
	if total == 0 || count <= 0 {
		return nil
	}
	// 要的数量超过网段总容量时，最多只能给出全部去重后的地址
	if uint64(count) > total {
		count = int(total)
	}
	seen := make(map[string]struct{}, count)
	out := make([]Result, 0, count)
	// 随机碰撞下重试要有上限，否则小网段可能一直抽到重复地址
	maxAttempts := count*50 + 100
	for len(out) < count && maxAttempts > 0 {
		maxAttempts--
		// 第一步：按网段容量加权选中一个网段
		pick := rng.Uint64() % total
		var chosen *net.IPNet
		for _, n := range ranges {
			ones, bits := n.Mask.Size()
			size := uint64(1) << (bits - ones)
			if pick < size {
				chosen = n
				break
			}
			pick -= size
		}
		if chosen == nil {
			break
		}
		ones, bits := chosen.Mask.Size()
		ip := make(net.IP, len(chosen.IP))
		copy(ip, chosen.IP)
		offset := rng.Uint64() % (uint64(1) << (bits - ones))
		// 从网段基地址起按 offset 递增
		for i := len(ip) - 1; i >= 0 && offset > 0; i-- {
			v := uint64(ip[i]) + (offset & 0xff)
			ip[i] = byte(v & 0xff)
			offset = (offset >> 8) + (v >> 8)
		}
		key := ip.String()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, Result{IP: key, Port: port})
	}
	return out
}
