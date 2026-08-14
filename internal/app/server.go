package app

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed all:web
var webFS embed.FS

// Server 提供 Web 图形界面与 REST 接口
type Server struct {
	runner *Runner
	sched  *Scheduler
	mux    *http.ServeMux
}

// NewServer 构造 Web 服务
func NewServer() *Server {
	s := &Server{runner: NewRunner(), mux: http.NewServeMux()}
	s.sched = NewScheduler(s.runner)
	s.sched.Start()
	s.routes()
	return s
}

func (s *Server) routes() {
	sub, err := fs.Sub(webFS, "web")
	if err == nil {
		s.mux.Handle("/", http.FileServer(http.FS(sub)))
	}
	s.mux.HandleFunc("/api/colos", s.handleColos)
	s.mux.HandleFunc("/api/config", s.handleConfig)
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/api/start", s.handleStart)
	s.mux.HandleFunc("/api/cancel", s.handleCancel)
	s.mux.HandleFunc("/api/events", s.handleEvents)
	s.mux.HandleFunc("/api/results", s.handleResults)
	s.mux.HandleFunc("/api/proxy-list", s.handleProxyList)
	s.mux.HandleFunc("/api/upload/api", s.handleUploadAPI)
	s.mux.HandleFunc("/api/upload/github", s.handleUploadGitHub)
	s.mux.HandleFunc("/api/cron", s.handleCron)
	s.mux.HandleFunc("/api/download", s.handleDownload)
	s.mux.HandleFunc("/api/system", s.handleSystem)
	s.mux.HandleFunc("/api/proxy-import", s.handleProxyImport)
	// 内置定时任务与候选来源合成
	s.mux.HandleFunc("GET /api/schedules", s.handleSchedulesList)
	s.mux.HandleFunc("POST /api/schedules", s.handleScheduleCreate)
	s.mux.HandleFunc("PUT /api/schedules/{id}", s.handleScheduleUpdate)
	s.mux.HandleFunc("DELETE /api/schedules/{id}", s.handleScheduleDelete)
	s.mux.HandleFunc("POST /api/schedules/{id}/run", s.handleScheduleRun)
	s.mux.HandleFunc("POST /api/proxy-fetch", s.handleProxyFetch)
}

// ServeHTTP 实现 http.Handler
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func orEmptyQuotas(q []RegionQuota) []RegionQuota {
	if q == nil {
		return []RegionQuota{}
	}
	return q
}

func orEmptyFixed(f []FixedItem) []FixedItem {
	if f == nil {
		return []FixedItem{}
	}
	return f
}

// normalizeQuotas 清洗提交的配额：去掉 0 数量、空机场码、重复项
func normalizeQuotas(q []RegionQuota) []RegionQuota {
	out := make([]RegionQuota, 0, len(q))
	seen := map[string]string{} // name -> codes joined
	for _, r := range q {
		if r.Count <= 0 || len(r.Colos) == 0 {
			continue
		}
		key := r.Name + "|" + strings.Join(r.Colos, ",")
		if seen[key] != "" {
			continue
		}
		seen[key] = key
		out = append(out, RegionQuota{Name: r.Name, Colos: r.Colos, Count: r.Count})
	}
	return out
}

// normalizeFixed 清洗固定附带列表：去掉空地址条目
func normalizeFixed(f []FixedItem) []FixedItem {
	out := make([]FixedItem, 0, len(f))
	for _, it := range f {
		if strings.TrimSpace(it.Addr) == "" {
			continue
		}
		out = append(out, FixedItem{Addr: strings.TrimSpace(it.Addr), Name: strings.TrimSpace(it.Name)})
	}
	return out
}

func (s *Server) handleColos(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, Colos)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c := LoadConfig()
		// 凭据只回传是否已设置，不回显明文
		writeJSON(w, http.StatusOK, map[string]any{
			"worker_domain":    c.WorkerDomain,
			"uuid":             c.UUID,
			"github_repo":      c.GitHubRepo,
			"github_path":      c.GitHubPath,
			"has_github_token": c.GitHubToken != "",
			"colo":             c.Colo,
			"ipv6":             c.IPv6,
			"count":            c.Count,
			"speed_limit":      c.SpeedLimit,
			"delay_limit":      c.DelayLimit,
			"threads":          c.Threads,
			"test_url":         c.TestURL,
			"sample_size":      c.SampleSize,
			"httping":          c.HTTPing,
			"disable_dl":       c.DisableDL,
			"port":             c.Port,
			"dl_timeout":       c.DLTimeout,
			"max_runtime":      c.MaxRunTime,
			"download_all":     c.DownloadAll,
			"region_quotas":    orEmptyQuotas(c.RegionQuotas),
			"other_quota":      c.OtherQuota,
			"quota_fill":       c.QuotaFill,
			"fixed_items":      orEmptyFixed(c.FixedItems),
		})
	case http.MethodPost:
		var in struct {
			WorkerDomain *string        `json:"worker_domain"`
			UUID         *string        `json:"uuid"`
			GitHubToken  *string        `json:"github_token"`
			GitHubRepo   *string        `json:"github_repo"`
			GitHubPath   *string        `json:"github_path"`
			Colo         *string        `json:"colo"`
			IPv6         *bool          `json:"ipv6"`
			Count        *int           `json:"count"`
			SpeedLimit   *float64       `json:"speed_limit"`
			DelayLimit   *int           `json:"delay_limit"`
			Threads      *int           `json:"threads"`
			TestURL      *string        `json:"test_url"`
			Port         *int           `json:"port"`
			DownloadAll  *bool          `json:"download_all"`
			RegionQuotas *[]RegionQuota `json:"region_quotas"`
			OtherQuota   *int           `json:"other_quota"`
			QuotaFill    *bool          `json:"quota_fill"`
			FixedItems   *[]FixedItem   `json:"fixed_items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		err := mutateConfig(func(cur *Config) {
			if in.WorkerDomain != nil {
				cur.WorkerDomain = *in.WorkerDomain
			}
			if in.UUID != nil {
				cur.UUID = *in.UUID
			}
			if in.GitHubToken != nil && *in.GitHubToken != "" {
				cur.GitHubToken = *in.GitHubToken
			}
			if in.GitHubRepo != nil {
				cur.GitHubRepo = *in.GitHubRepo
			}
			if in.GitHubPath != nil {
				cur.GitHubPath = *in.GitHubPath
			}
			if in.Colo != nil {
				cur.Colo = *in.Colo
			}
			if in.IPv6 != nil {
				cur.IPv6 = *in.IPv6
			}
			if in.Count != nil {
				cur.Count = *in.Count
			}
			if in.SpeedLimit != nil {
				cur.SpeedLimit = *in.SpeedLimit
			}
			if in.DelayLimit != nil {
				cur.DelayLimit = *in.DelayLimit
			}
			if in.Threads != nil {
				cur.Threads = *in.Threads
			}
			if in.TestURL != nil {
				cur.TestURL = *in.TestURL
			}
			if in.Port != nil {
				cur.Port = *in.Port
			}
			if in.DownloadAll != nil {
				cur.DownloadAll = *in.DownloadAll
			}
			if in.RegionQuotas != nil {
				cur.RegionQuotas = normalizeQuotas(*in.RegionQuotas)
			}
			if in.OtherQuota != nil {
				if *in.OtherQuota < 0 {
					cur.OtherQuota = 0
				} else {
					cur.OtherQuota = *in.OtherQuota
				}
			}
			if in.QuotaFill != nil {
				cur.QuotaFill = *in.QuotaFill
			}
			if in.FixedItems != nil {
				cur.FixedItems = normalizeFixed(*in.FixedItems)
			}
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case http.MethodDelete:
		if err := ClearConfig(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "不支持的方法")
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"running": s.runner.Running(),
		"count":   len(s.runner.Results()),
	})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "请用 POST")
		return
	}
	var o Options
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if !s.runner.Start(o) {
		writeErr(w, http.StatusConflict, "已有测速任务在运行")
		return
	}
	// 记住这次参数，下次打开界面自动回填
	_ = mutateConfig(func(c *Config) {
		c.Colo, c.IPv6, c.Count = o.Colo, o.IPv6, o.Count
		c.SpeedLimit, c.DelayLimit, c.Threads = o.SpeedLimit, o.DelayLimit, o.Threads
		c.TestURL, c.Port = o.TestURL, o.Port
		c.SampleSize, c.HTTPing, c.DisableDL = o.SampleSize, o.HTTPing, o.DisableDL
		c.DLTimeout, c.MaxRunTime, c.DownloadAll = o.DLTimeout, o.MaxRunTime, o.DownloadAll
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	s.runner.Cancel()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "服务端不支持流式输出")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsub := s.runner.Subscribe()
	defer unsub()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.runner.Results())
}

func (s *Server) handleProxyList(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Limit int `json:"limit"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	rs := s.runner.Results()
	if len(rs) == 0 {
		var err error
		if rs, err = ReadCSV(ResultFile); err != nil {
			writeErr(w, http.StatusBadRequest, "没有可用的测速结果")
			return
		}
	}
	n, err := WriteProxyList(ProxyListFile, rs, in.Limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n, "file": ProxyListFile})
}

func (s *Server) resultsOrCSV(w http.ResponseWriter) ([]Result, bool) {
	rs := s.runner.Results()
	if len(rs) > 0 {
		return rs, true
	}
	rs, err := ReadCSV(ResultFile)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "没有可用的测速结果")
		return nil, false
	}
	return rs, true
}

func (s *Server) handleUploadAPI(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Domain string `json:"worker_domain"`
		UUID   string `json:"uuid"`
		Limit  int    `json:"limit"`
		Clear  bool   `json:"clear"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	c := LoadConfig()
	if in.Domain == "" {
		in.Domain = c.WorkerDomain
	}
	if in.UUID == "" {
		in.UUID = c.UUID
	}
	rs, ok := s.resultsOrCSV(w)
	if !ok {
		return
	}
	// 装配：全局配额 + 固定附带列表
	plan := UploadPlan{
		Quotas:     c.RegionQuotas,
		OtherQuota: c.OtherQuota,
		QuotaFill:  c.QuotaFill,
		Fixed:      c.FixedItems,
		Limit:      in.Limit,
	}
	rs, warns := BuildUploadList(rs, plan)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	n, err := UploadToAPI(ctx, APITarget{Domain: in.Domain, UUID: in.UUID}, rs, 0, in.Clear)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = mutateConfig(func(c *Config) { c.WorkerDomain, c.UUID = in.Domain, in.UUID })
	writeJSON(w, http.StatusOK, map[string]any{"count": n, "warnings": warns})
}

func (s *Server) handleUploadGitHub(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Repo  string `json:"repo"`
		Token string `json:"token"`
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	c := LoadConfig()
	if in.Repo == "" {
		in.Repo = c.GitHubRepo
	}
	if in.Token == "" {
		in.Token = c.GitHubToken
	}
	if in.Path == "" {
		in.Path = c.GitHubPath
	}
	rs, ok := s.resultsOrCSV(w)
	if !ok {
		return
	}
	plan := UploadPlan{
		Quotas:     c.RegionQuotas,
		OtherQuota: c.OtherQuota,
		QuotaFill:  c.QuotaFill,
		Fixed:      c.FixedItems,
		Limit:      in.Limit,
	}
	rs, warns := BuildUploadList(rs, plan)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	n, err := UploadToGitHub(ctx, GitHubTarget{Repo: in.Repo, Token: in.Token, Path: in.Path}, rs, 0)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = mutateConfig(func(c *Config) {
		c.GitHubRepo, c.GitHubToken, c.GitHubPath = in.Repo, in.Token, in.Path
	})
	writeJSON(w, http.StatusOK, map[string]any{"count": n, "warnings": warns})
}

// handleSystem 返回运行环境信息，供界面决定展示哪些功能
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	writeJSON(w, http.StatusOK, map[string]any{
		"cron_supported":   CronSupported(),
		"self_path":        SelfPath(),
		"result_file":      ResultFile,
		"proxy_file":       ProxyListFile,
		"default_url":      DefaultTestURL,
		"server_time":      now.Unix(),
		"server_tz":        now.Location().String(),
		"server_time_text": now.Format("15:04"),
	})
}

// handleCron 管理定时任务
func (s *Server) handleCron(w http.ResponseWriter, r *http.Request) {
	if !CronSupported() {
		writeErr(w, http.StatusNotImplemented, "当前系统不支持 crontab，请用系统自带的计划任务")
		return
	}
	switch r.Method {
	case http.MethodGet:
		jobs, err := ListCronJobs()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([]map[string]string, 0, len(jobs))
		for _, j := range jobs {
			out = append(out, map[string]string{"schedule": j.Schedule, "command": j.Command})
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		var in struct {
			Schedule string `json:"schedule"`
			Args     string `json:"args"`
			Replace  bool   `json:"replace"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		if strings.TrimSpace(in.Args) == "" {
			writeErr(w, http.StatusBadRequest, "请填写测速参数")
			return
		}
		self := SelfPath()
		cmd := fmt.Sprintf("cd %s && %s %s >> yx-cron.log 2>&1",
			shellQuote(DataDir()), shellQuote(self), in.Args)
		if err := AddCronJob(in.Schedule, cmd, in.Replace); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "command": cmd})

	case http.MethodDelete:
		n, err := RemoveCronJobs()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": n})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "不支持的方法")
	}
}

// handleProxyImport 接收一份外部 CSV 或 IP:端口 文本，生成反代列表。
// 对应旧 Python 版的优选反代第一步：把别人分享的结果转成可测的列表。
func (s *Server) handleProxyImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "请用 POST")
		return
	}
	var in struct {
		Text string `json:"text"`
		Take int    `json:"take"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	rs, err := ParseProxySource(in.Text)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := WriteProxyList(ProxyListFile, rs, in.Take)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n, "file": ProxyListFile})
}

// handleDownload 下载测速结果或反代列表
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	var name string
	switch kind {
	case "proxy":
		name = ProxyListFile
	default:
		name = ResultFile
	}
	data, err := os.ReadFile(DataPath(name))
	if err != nil {
		writeErr(w, http.StatusNotFound, "文件不存在，请先测速")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	_, _ = w.Write(data)
}

// shellQuote 给含空格的路径加引号
func shellQuote(s string) string {
	if strings.ContainsAny(s, " \t") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// ── 内置定时任务 ──────────────────────────────────

// handleSchedulesList 列出全部定时任务
func (s *Server) handleSchedulesList(w http.ResponseWriter, r *http.Request) {
	c := LoadConfig()
	writeJSON(w, http.StatusOK, c.Schedules)
}

// validateScheduleTiming 校验任务的执行时间设置，并就地归一化。
// daily 模式至少要有一个合法时刻；间隔模式频率至少 1 分钟。
func validateScheduleTiming(in *ScheduleTask) error {
	if in.Mode == "daily" {
		in.Times = validTimePoints(in.Times)
		if len(in.Times) == 0 {
			return fmt.Errorf("每天固定时刻至少填一个合法的 HH:MM")
		}
		return nil
	}
	in.Mode = "" // 非 daily 一律视为间隔模式，保持字段干净
	if in.IntervalMin < 1 {
		return fmt.Errorf("执行频率至少 1 分钟")
	}
	return nil
}

// handleScheduleCreate 新建任务：从当前面板复制参数，首次执行等一个周期
func (s *Server) handleScheduleCreate(w http.ResponseWriter, r *http.Request) {
	var in ScheduleTask
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := validateScheduleTiming(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	in.ID = genID()
	now := time.Now()
	in.LastRun = 0
	in.NextRun = nextRun(&in, now)
	in.LastLog = fmt.Sprintf("%s 任务已创建", stamp())
	var out ScheduleTask
	err := mutateConfig(func(c *Config) {
		c.Schedules = append(c.Schedules, in)
		normalizeSchedule(&c.Schedules[len(c.Schedules)-1], len(c.Schedules)-1)
		out = c.Schedules[len(c.Schedules)-1]
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleScheduleUpdate 整体覆盖一条任务；编辑后下次执行时间重新起算
func (s *Server) handleScheduleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in ScheduleTask
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if in.ID != "" && in.ID != id {
		writeErr(w, http.StatusBadRequest, "任务 ID 不匹配")
		return
	}
	if in.IntervalMin < 1 && in.Mode != "daily" {
		writeErr(w, http.StatusBadRequest, "执行频率至少 1 分钟")
		return
	}
	if err := validateScheduleTiming(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var out ScheduleTask
	found := false
	err := mutateConfig(func(c *Config) {
		for i := range c.Schedules {
			if c.Schedules[i].ID != id {
				continue
			}
			in.ID = id
			in.LastRun = c.Schedules[i].LastRun // 编辑不抹掉执行历史
			in.NextRun = nextRun(&in, time.Now())
			normalizeSchedule(&in, i)
			c.Schedules[i] = in
			out = in
			found = true
			break
		}
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleScheduleDelete 删除一条任务
func (s *Server) handleScheduleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	removed := 0
	err := mutateConfig(func(c *Config) {
		kept := c.Schedules[:0]
		for _, t := range c.Schedules {
			if t.ID == id {
				removed++
				continue
			}
			kept = append(kept, t)
		}
		c.Schedules = kept
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if removed == 0 {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleScheduleRun 立即执行一条任务；撞车时由调度器跳过并记日志
func (s *Server) handleScheduleRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.sched.RunNow(id); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleProxyFetch 抓取 URL、合并文本与随机 CF，生成候选列表并预览统计。
// save 为真时把合并结果写进 ips_ports.txt，供随后的反代测速使用。
func (s *Server) handleProxyFetch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URLs          []string `json:"urls"`
		Text          string   `json:"text"`
		RandomCF      bool     `json:"random_cf"`
		RandomCFCount int      `json:"random_cf_count"`
		Port          int      `json:"port"`
		Take          int      `json:"take"`
		Save          bool     `json:"save"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(in.URLs) > 10 {
		writeErr(w, http.StatusBadRequest, "最多填 10 个 URL")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	items, stats, err := BuildCandidateSources(ctx, ScheduleSources{
		URLs: in.URLs, Text: in.Text,
		RandomCF: in.RandomCF, RandomCFCount: in.RandomCFCount,
	}, in.Port)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": err.Error(), "sources": stats,
		})
		return
	}
	if in.Save {
		if _, err := WriteProxyList(ProxyListFile, items, in.Take); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	n := len(items)
	if in.Take > 0 && in.Take < n {
		n = in.Take
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":   n,
		"file":    ProxyListFile,
		"sources": stats,
	})
}
