package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// UploadPlan 描述一次上传的装配规则：分地区配额与固定附带列表。
type UploadPlan struct {
	Quotas     []RegionQuota // 地区配额；非空时忽略 Limit，按配额总量控制
	OtherQuota int           // 「其他地区」数量，0 表示不额外传
	QuotaFill  bool          // 地区不足时用剩余结果补位
	Fixed      []FixedItem   // 固定附带条目，附加在最后
	Limit      int           // 无配额时的全局数量上限，0 表示全部
}

// BuildUploadList 把测速结果按地区配额与固定附带列表装配成最终上传列表。
// 返回装配后的结果与提示信息。结果本身已按速度降序。
func BuildUploadList(rs []Result, p UploadPlan) ([]Result, []string) {
	var warnings []string
	if len(p.Quotas) == 0 {
		out := rs
		if p.Limit > 0 && p.Limit < len(out) {
			out = out[:p.Limit]
		}
		return appendFixed(out, p.Fixed, &warnings), warnings
	}

	used := make([]bool, len(rs))
	var picked []Result
	regionCounts := make([]int, len(p.Quotas))

	// 1) 各地区按配额从自己的机场码结果里优先挑
	for qi, q := range p.Quotas {
		if q.Count <= 0 {
			continue
		}
		for i := range rs {
			if used[i] || regionCounts[qi] >= q.Count {
				continue
			}
			if q.ColoHit(rs[i].Colo) {
				used[i] = true
				picked = append(picked, rs[i])
				regionCounts[qi]++
			}
		}
	}

	// 2) 不足的地区：补位策略用剩余结果填满，否则少传
	for qi, q := range p.Quotas {
		if q.Count <= 0 || regionCounts[qi] >= q.Count {
			continue
		}
		if p.QuotaFill {
			for i := range rs {
				if used[i] || regionCounts[qi] >= q.Count {
					continue
				}
				used[i] = true
				picked = append(picked, rs[i])
				regionCounts[qi]++
			}
		}
		if regionCounts[qi] < q.Count {
			msg := fmt.Sprintf("%s 配额 %d 个，实际只有 %d 个", q.Name, q.Count, regionCounts[qi])
			if p.QuotaFill {
				msg += "（已用其他地区补位）"
			} else {
				msg += "（不足就少传）"
			}
			warnings = append(warnings, msg)
		}
	}

	// 3) 「其他地区」行：从还没用到的、不属于任何配额地区的结果里取前 N
	if p.OtherQuota > 0 {
		n := 0
		for i := range rs {
			if n >= p.OtherQuota || used[i] {
				continue
			}
			if !matchesAnyQuota(rs[i].Colo, p.Quotas) {
				used[i] = true
				picked = append(picked, rs[i])
				n++
			}
		}
		// 仍不够就从所有剩余里补
		for i := range rs {
			if n >= p.OtherQuota || used[i] {
				continue
			}
			used[i] = true
			picked = append(picked, rs[i])
			n++
		}
	}

	// 提示：有没有结果因为缺地区信息而走不进配额
	noColoPicked := 0
	for i := range rs {
		if used[i] && rs[i].Colo == "" {
			noColoPicked++
		}
	}
	if noColoPicked > 0 {
		warnings = append(warnings, fmt.Sprintf("%d 条结果没有地区信息，被归入补位/其他地区；建议用「真实连接」测法", noColoPicked))
	}

	return appendFixed(picked, p.Fixed, &warnings), warnings
}

// matchesAnyQuota 判断机场码是否命中任一配额地区
func matchesAnyQuota(colo string, quotas []RegionQuota) bool {
	for _, q := range quotas {
		if q.ColoHit(colo) {
			return true
		}
	}
	return false
}

// appendFixed 把固定附带条目去重后追加在列表最后，并返回提示。
func appendFixed(out []Result, fixed []FixedItem, warnings *[]string) []Result {
	if len(fixed) == 0 {
		return out
	}
	type fe struct {
		host string
		port int
		name string
	}
	var parsed []fe
	hosts := map[string]bool{}
	for _, f := range fixed {
		host, port, ok := parseFixedAddr(f.Addr)
		if !ok {
			*warnings = append(*warnings, "固定条目无法解析，已跳过: "+f.Addr)
			continue
		}
		if hosts[host] {
			continue // 重复条目只留第一条
		}
		hosts[host] = true
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = host
		}
		parsed = append(parsed, fe{host: host, port: port, name: name})
	}
	if len(parsed) == 0 {
		return out
	}
	// 与测速结果重复的 IP 去掉结果，保留固定条目（带名字）
	filtered := make([]Result, 0, len(out))
	for _, r := range out {
		if hosts[r.IP] {
			continue
		}
		filtered = append(filtered, r)
	}
	for _, f := range parsed {
		filtered = append(filtered, Result{IP: f.host, Port: f.port, ColoName: f.name, Fixed: true})
	}
	return filtered
}

// parseFixedAddr 解析固定条目的地址：IP、IP:端口、域名、域名:端口。
// 端口缺省 443。
func parseFixedAddr(s string) (host string, port int, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t/") {
		return "", 0, false
	}
	port = 443
	if strings.HasPrefix(s, "[") { // IPv6 字面量 [::1]:443
		if h, p, err := net.SplitHostPort(s); err == nil {
			pp, _ := strconv.Atoi(p)
			if pp > 0 && pp < 65536 {
				port = pp
			}
			return h, port, net.ParseIP(h) != nil
		}
		return "", 0, false
	}
	if strings.Count(s, ":") == 1 {
		h, p, err := net.SplitHostPort(s)
		if err == nil {
			pp, _ := strconv.Atoi(p)
			if pp > 0 && pp < 65536 && h != "" {
				return h, pp, true
			}
		}
	}
	if net.ParseIP(s) != nil {
		return s, port, true
	}
	// 域名（无端口）
	if strings.Count(s, ":") == 0 {
		return s, port, true
	}
	return "", 0, false
}

// APITarget 描述 cfnew 的优选 IP 接口位置
type APITarget struct {
	Domain string // Worker 域名，如 example.workers.dev
	UUID   string // UUID 或自定义路径
}

func (t APITarget) url() string {
	d := strings.TrimSpace(t.Domain)
	scheme := "https"
	// 允许显式指定 http，主要用于本地或内网自建
	if strings.HasPrefix(strings.ToLower(d), "http://") {
		scheme = "http"
		d = d[len("http://"):]
	} else if strings.HasPrefix(strings.ToLower(d), "https://") {
		d = d[len("https://"):]
	}
	d = strings.TrimSuffix(d, "/")
	// 去掉可能带上的路径部分
	if i := strings.Index(d, "/"); i >= 0 {
		d = d[:i]
	}
	u := strings.Trim(strings.TrimSpace(t.UUID), "/")
	return fmt.Sprintf("%s://%s/%s/api/preferred-ips", scheme, d, u)
}

type apiItem struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
	Name string `json:"name"`
}

// CountRemoteIPs 查询远端已有的优选 IP 数量
func CountRemoteIPs(ctx context.Context, t APITarget) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 160))
	}
	var out struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(body, &out)
	return out.Count, nil
}

// ClearRemoteIPs 清空远端优选 IP
func ClearRemoteIPs(ctx context.Context, t APITarget) error {
	payload, _ := json.Marshal(map[string]bool{"all": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.url(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("清空失败 HTTP %d: %s", resp.StatusCode, truncate(string(body), 160))
	}
	return nil
}

// UploadToAPI 批量上报优选 IP 到 cfnew
func UploadToAPI(ctx context.Context, t APITarget, rs []Result, limit int, clear bool) (int, error) {
	if strings.TrimSpace(t.Domain) == "" || strings.TrimSpace(t.UUID) == "" {
		return 0, fmt.Errorf("请先填写 Worker 域名和 UUID")
	}
	if limit > 0 && limit < len(rs) {
		rs = rs[:limit]
	}
	if len(rs) == 0 {
		return 0, fmt.Errorf("没有可上传的结果")
	}
	if clear {
		if err := ClearRemoteIPs(ctx, t); err != nil {
			return 0, err
		}
	}
	items := make([]apiItem, 0, len(rs))
	for _, r := range rs {
		port := r.Port
		if port <= 0 {
			port = 443
		}
		items = append(items, apiItem{IP: r.IP, Port: port, Name: nodeName(r)})
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url(), bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("上传失败 HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return len(items), nil
}

// nodeName 生成节点备注，如「香港-8.34MB/s」。
// 沿用旧 Python 版的格式：选优选 IP 看的是速度，延迟放名字里参考价值低。
// 固定附带条目用用户起的名字，不加速度后缀。
func nodeName(r Result) string {
	if r.Fixed {
		if r.ColoName == "" {
			return r.IP
		}
		return r.ColoName
	}
	name := ColoName(r.Colo)
	if name == "未知" {
		name = "未知地区"
	}
	return fmt.Sprintf("%s-%.2fMB/s", name, r.Speed)
}

// GitHubTarget 描述 GitHub 上传位置
type GitHubTarget struct {
	Repo  string // owner/repo
	Token string
	Path  string // 仓库内文件路径
}

// UploadToGitHub 把优选列表写入 GitHub 仓库，已存在则更新
func UploadToGitHub(ctx context.Context, t GitHubTarget, rs []Result, limit int) (int, error) {
	repo := strings.Trim(strings.TrimSpace(t.Repo), "/")
	if repo == "" || strings.TrimSpace(t.Token) == "" {
		return 0, fmt.Errorf("请先填写 GitHub 仓库和 Token")
	}
	if !strings.Contains(repo, "/") {
		return 0, fmt.Errorf("仓库格式应为 owner/repo")
	}
	path := strings.TrimSpace(t.Path)
	if path == "" {
		path = "cloudflare_ips.txt"
	}
	if limit > 0 && limit < len(rs) {
		rs = rs[:limit]
	}
	if len(rs) == 0 {
		return 0, fmt.Errorf("没有可上传的结果")
	}

	var sb strings.Builder
	for _, r := range rs {
		port := r.Port
		if port <= 0 {
			port = 443
		}
		fmt.Fprintf(&sb, "%s:%d#%s\n", r.IP, port, nodeName(r))
	}
	content := base64.StdEncoding.EncodeToString([]byte(sb.String()))
	api := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repo, path)

	// 已存在则需要带上 sha 才能更新
	sha := ""
	{
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
		req.Header.Set("Authorization", "Bearer "+t.Token)
		req.Header.Set("Accept", "application/vnd.github+json")
		if resp, err := httpClient.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var meta struct {
					SHA string `json:"sha"`
				}
				b, _ := io.ReadAll(resp.Body)
				_ = json.Unmarshal(b, &meta)
				sha = meta.SHA
			}
		}
	}

	payload := map[string]string{
		"message": fmt.Sprintf("更新优选 IP (%d 个) %s", len(rs), time.Now().Format("2006-01-02 15:04")),
		"content": content,
	}
	if sha != "" {
		payload["sha"] = sha
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, api, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+t.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("GitHub 上传失败 HTTP %d: %s", resp.StatusCode, truncate(string(rb), 200))
	}
	return len(rs), nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
