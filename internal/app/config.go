package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Config 是持久化到磁盘的用户配置
type Config struct {
	WorkerDomain string `json:"worker_domain"`
	UUID         string `json:"uuid"`
	GitHubToken  string `json:"github_token"`
	GitHubRepo   string `json:"github_repo"`
	GitHubPath   string `json:"github_path"`

	// 上次使用的测速参数，供界面回填
	Colo        string  `json:"colo"`
	IPv6        bool    `json:"ipv6"`
	Count       int     `json:"count"`
	SpeedLimit  float64 `json:"speed_limit"`
	DelayLimit  int     `json:"delay_limit"`
	Threads     int     `json:"threads"`
	TestURL     string  `json:"test_url"`
	Port        int     `json:"port"`
	SampleSize  int     `json:"sample_size"`
	HTTPing     bool    `json:"httping"`
	DisableDL   bool    `json:"disable_dl"`
	DLTimeout   int     `json:"dl_timeout"`
	MaxRunTime  int     `json:"max_runtime"`
	DownloadAll bool    `json:"download_all"` // 下载阶段全部测完再按速度取前 N

	// 内置定时任务（Docker 等没有 crontab 的环境也靠它）
	Schedules []ScheduleTask `json:"schedules"`

	// 分地区上传配额与固定附带列表（全局，手动与定时上传共用）
	RegionQuotas []RegionQuota `json:"region_quotas"`
	OtherQuota   int           `json:"other_quota"` // 「其他地区」上传数量，0 表示不额外传
	QuotaFill    bool          `json:"quota_fill"`  // 地区不足时用其他地区补位
	FixedItems   []FixedItem   `json:"fixed_items"` // 每次上传都必定附带的 IP/域名
}

// RegionQuota 一个地区的上传配额：结果机场码命中 Colos 里任何一个就算该地区
type RegionQuota struct {
	Name  string   `json:"name"`
	Colos []string `json:"colos"`
	Count int      `json:"count"` // 0 表示未配置该地区
}

// ColoHit 判断结果机场码是否属于该配额地区
func (q RegionQuota) ColoHit(colo string) bool {
	if colo == "" || len(q.Colos) == 0 {
		return false
	}
	for _, c := range q.Colos {
		if strings.EqualFold(c, colo) {
			return true
		}
	}
	return false
}

// FixedItem 每次上传都固定附带的条目；Addr 为 IP、IP:端口、域名或 域名:端口
type FixedItem struct {
	Addr string `json:"addr"`
	Name string `json:"name"`
}

// ScheduleSources 定时任务的候选 IP 来源：
// URL 列表每次执行前重新抓取，文本与随机 CF 一起合并去重。
type ScheduleSources struct {
	URLs          []string `json:"urls"`            // 每行一个 IP 列表的链接
	Text          string   `json:"text"`            // 粘贴的 IP/IP:端口 列表
	RandomCF      bool     `json:"random_cf"`       // 随机混入 Cloudflare 官方 IPv4 段
	RandomCFCount int      `json:"random_cf_count"` // 随机生成的 IP 数量
}

// ScheduleUpload 定时任务的上报设置，GitHub 与 Worker 可同时开
type ScheduleUpload struct {
	GitHub      bool `json:"github"`       // 上传到 GitHub 仓库
	Worker      bool `json:"worker"`       // 上报到 cfnew Worker
	TopN        int  `json:"top_n"`        // 优中选优，取前 N 个，0 表示全部
	WorkerClear bool `json:"worker_clear"` // 上报前清空 Worker 已有 IP

	// 分地区上传配额快照；配了配额就以配额总量为准，忽略 TopN
	Quotas     []RegionQuota `json:"quotas"`
	OtherQuota int           `json:"other_quota"`
	QuotaFill  bool          `json:"quota_fill"`
}

// ScheduleTask 是一条内置定时任务：到点测速并按设置上报
type ScheduleTask struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Enabled     bool            `json:"enabled"`
	Mode        string          `json:"mode"` // "daily" 每天固定时刻；其他值/缺省为间隔模式
	IntervalMin int             `json:"interval_minutes"`
	Times       []string        `json:"times"`    // daily 模式的 HH:MM 列表
	LastRun     int64           `json:"last_run"` // Unix 秒，0 表示没跑过
	NextRun     int64           `json:"next_run"` // Unix 秒，0 表示到期立即补跑
	LastLog     string          `json:"last_log"`
	Opts        Options         `json:"opts"`    // 测速参数快照
	Sources     ScheduleSources `json:"sources"` // 候选来源快照
	Upload      ScheduleUpload  `json:"upload"`  // 上报设置快照
}

const configName = "yx-config.json"

var (
	cfgMu sync.RWMutex
	cfg   *Config
)

// DefaultConfig 返回一份带默认值的配置
func DefaultConfig() *Config {
	return &Config{
		GitHubPath: "cloudflare_ips.txt",
		Count:      10,
		SpeedLimit: 1,
		DelayLimit: 1000,
		Threads:    200,
		TestURL:    DefaultTestURL,
		Port:       443,
		SampleSize: 1000,
		DLTimeout:  10,
	}
}

// configDirOverride 测试用：覆盖配置落盘目录，避免把真实用户的配置写坏
var configDirOverride string

// ConfigPath 返回配置文件路径，落在可写的数据目录里
func ConfigPath() string {
	if configDirOverride != "" {
		return filepath.Join(configDirOverride, configName)
	}
	return DataPath(configName)
}

// LoadConfig 读取磁盘配置，不存在时返回默认值。
// Schedules 做深拷贝：调度器与请求处理器会并发读写，不能让两边共享同一个切片。
func LoadConfig() *Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if cfg == nil {
		cfg = loadConfigFrom(ConfigPath())
	}
	c := *cfg
	c.Schedules = append([]ScheduleTask(nil), cfg.Schedules...)
	return &c
}

// 历史上用过、现在已经测不出速度的下载地址。
// 这些地址存进了老用户的配置里，升级后会被回填回来盖掉新默认值，
// 结果就是版本换了速度依然是 0，所以读取时直接迁移掉。
var deadTestURLs = []string{
	"cf.xiu2.xyz",           // 返回 403
	"cloudflaremirrors.com", // 返回 200 但 body 是空的
	// v3.0.3/v3.0.4 误把私人域名设成了默认值，用户配置里存下来的要清掉，
	// 否则升级后还在替域名主人烧流量
	"xy.kg",
}

func isDeadTestURL(u string) bool {
	for _, dead := range deadTestURLs {
		if strings.Contains(u, dead) {
			return true
		}
	}
	return false
}

// loadConfigFrom 从指定路径读配置并补齐缺省值。
// 独立出来是为了能直接测到失效地址的迁移。
func loadConfigFrom(path string) *Config {
	c := DefaultConfig()
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, c)
	}
	if c.TestURL == "" || isDeadTestURL(c.TestURL) {
		c.TestURL = DefaultTestURL
	}
	if c.Port <= 0 {
		c.Port = 443
	}
	if c.DLTimeout <= 0 {
		c.DLTimeout = 10
	}
	for i := range c.Schedules {
		normalizeSchedule(&c.Schedules[i], i)
	}
	return c
}

// normalizeSchedule 补齐一条定时任务的缺省值与边界
func normalizeSchedule(t *ScheduleTask, idx int) {
	if t.IntervalMin < 1 {
		t.IntervalMin = 360
	}
	if t.Upload.TopN < 0 {
		t.Upload.TopN = 0
	}
	if t.Sources.RandomCF && t.Sources.RandomCFCount <= 0 {
		t.Sources.RandomCFCount = 100
	}
	if strings.TrimSpace(t.Name) == "" {
		t.Name = fmt.Sprintf("任务 %d", idx+1)
	}
	if t.Mode == "daily" {
		t.Times = validTimePoints(t.Times)
		if len(t.Times) == 0 {
			t.Times = []string{"08:00"}
		}
	}
}

// parseTimePoint 解析 HH:MM，返回小时与分钟
func parseTimePoint(s string) (hh, mm int, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	hh, err1 := strconv.Atoi(parts[0])
	mm, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

// validTimePoints 过滤出合法的时间点，统一格式化为 HH:MM 并去重
func validTimePoints(times []string) []string {
	out := make([]string, 0, len(times))
	seen := map[string]bool{}
	for _, t := range times {
		hh, mm, ok := parseTimePoint(t)
		if !ok {
			continue
		}
		norm := fmt.Sprintf("%02d:%02d", hh, mm)
		if !seen[norm] {
			out = append(out, norm)
			seen[norm] = true
		}
	}
	return out
}

// SaveConfig 覆盖写入配置
func SaveConfig(c *Config) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	cfg = c
	return saveConfigLocked()
}

// mutateConfig 在锁内修改全局配置并落盘。
// 与「读副本-改-写回」不同，它直接改全局对象，调度器的并发更新不会被覆盖丢失。
func mutateConfig(fn func(*Config)) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if cfg == nil {
		cfg = loadConfigFrom(ConfigPath())
	}
	fn(cfg)
	return saveConfigLocked()
}

// updateTask 按 ID 找到一条定时任务，在锁内应用 fn 并落盘。
// 调度器用它更新 LastRun/NextRun/LastLog，避免用陈旧的配置副本覆盖用户刚改的设置。
func updateTask(id string, fn func(*ScheduleTask)) bool {
	found := false
	_ = mutateConfig(func(c *Config) {
		for i := range c.Schedules {
			if c.Schedules[i].ID != id {
				continue
			}
			fn(&c.Schedules[i])
			normalizeSchedule(&c.Schedules[i], i)
			found = true
			break
		}
	})
	return found
}

// saveConfigLocked 序列化并写盘；调用方必须持有 cfgMu
func saveConfigLocked() error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), data, 0o600)
}

// ClearConfig 删除磁盘上的配置文件
func ClearConfig() error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	cfg = DefaultConfig()
	err := os.Remove(ConfigPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
