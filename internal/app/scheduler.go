package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// runExecutor 是调度器执行测速任务所需的接口。
// 抽出接口是为了单测时注入假实现，不用真跑网络测速。
type runExecutor interface {
	Running() bool
	StartWithDone(o Options, onDone func([]Result, error)) bool
	Log(msg string)
}

// Scheduler 是程序内置的定时调度器，Docker 等没有 crontab 的环境靠它。
// 每 30 秒检查一次到期任务；任务串行执行，撞到正在跑的任务就跳过。
type Scheduler struct {
	mu           sync.Mutex
	exec         runExecutor
	stop         chan struct{}
	stopOnce     sync.Once
	tick         time.Duration
	uploadGitHub func(context.Context, GitHubTarget, []Result, int) (int, error)
	uploadAPI    func(context.Context, APITarget, []Result, int, bool) (int, error)
}

// NewScheduler 构造调度器；upload* 缺省用真实上报实现，测试可注入
func NewScheduler(exec runExecutor) *Scheduler {
	return &Scheduler{
		exec:         exec,
		stop:         make(chan struct{}),
		tick:         30 * time.Second,
		uploadGitHub: UploadToGitHub,
		uploadAPI:    UploadToAPI,
	}
}

// Start 启动后台检查循环
func (s *Scheduler) Start() {
	go func() {
		t := time.NewTicker(s.tick)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.CheckDue()
			}
		}
	}()
}

// Stop 停止后台循环；幂等
func (s *Scheduler) Stop() { s.stopOnce.Do(func() { close(s.stop) }) }

// CheckDue 跑一遍到期任务；同时只会有一个执行入口（互斥锁兜底）
func (s *Scheduler) CheckDue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := LoadConfig()
	now := time.Now().Unix()
	for i := range c.Schedules {
		t := &c.Schedules[i]
		if !t.Enabled || (t.NextRun != 0 && now < t.NextRun) {
			continue
		}
		s.runOne(&c.Schedules[i])
	}
}

// RunNow 立即执行一条任务，供界面「立即执行」按钮使用
func (s *Scheduler) RunNow(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := LoadConfig()
	for i := range c.Schedules {
		if c.Schedules[i].ID != id {
			continue
		}
		s.runOne(&c.Schedules[i])
		return nil
	}
	return fmt.Errorf("任务不存在")
}

// runOne 执行一条任务：合成来源 → 写候选列表 → 测速 → 上报。
// 调用方必须持有 s.mu。
func (s *Scheduler) runOne(t *ScheduleTask) {
	if s.exec.Running() {
		s.logTask(t, "到点触发，但已有测速任务在运行，本轮跳过")
		return
	}
	// 来源合成：URL 每次重新抓取，失败的跳过
	fctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	items, stats, err := BuildCandidateSources(fctx, t.Sources, t.Opts.Port)
	cancel()
	if err != nil {
		s.logTask(t, "候选来源加载失败: "+err.Error())
		return
	}
	s.exec.Log(fmt.Sprintf("定时任务「%s」：候选 IP 共 %d 条", t.Name, len(items)))
	for _, st := range stats {
		if st.Warning != "" {
			s.exec.Log(fmt.Sprintf("定时任务「%s」：%s 失败：%s", t.Name, st.Name, st.Warning))
		}
	}
	if _, err := WriteProxyList(ProxyListFile, items, 0); err != nil {
		s.logTask(t, "写入候选列表失败: "+err.Error())
		return
	}

	o := t.Opts
	o.Proxy = true
	o.IPFile = DataPath(ProxyListFile)
	o.IPText = ""

	done := make(chan struct{})
	started := s.exec.StartWithDone(o, func(rs []Result, runErr error) {
		defer close(done)
		s.finishRun(t, rs, runErr)
	})
	if !started {
		s.logTask(t, "到点触发，但已有测速任务在运行，本轮跳过")
		return
	}
	<-done
}

// finishRun 测速结束后按任务设置上报并记录日志
func (s *Scheduler) finishRun(t *ScheduleTask, rs []Result, runErr error) {
	cfg := LoadConfig()
	var lines []string
	if runErr != nil {
		lines = append(lines, fmt.Sprintf("%s 测速失败: %v", stamp(), runErr))
	} else if len(rs) == 0 {
		lines = append(lines, fmt.Sprintf("%s 测速完成，但没有任何结果，跳过上报", stamp()))
	} else {
		lines = append(lines, fmt.Sprintf("%s 测速完成，共 %d 个结果，优中选优取前 %d 个上报",
			stamp(), len(rs), uploadCount(t.Upload.TopN, len(rs))))
		if t.Upload.GitHub {
			s.exec.Log(fmt.Sprintf("定时任务「%s」：上传 GitHub…", t.Name))
			uctx, uc := context.WithTimeout(context.Background(), 60*time.Second)
			n, err := s.uploadGitHub(uctx, GitHubTarget{
				Repo: cfg.GitHubRepo, Token: cfg.GitHubToken, Path: cfg.GitHubPath,
			}, rs, t.Upload.TopN)
			uc()
			if err != nil {
				lines = append(lines, fmt.Sprintf("%s GitHub 上传失败: %v", stamp(), err))
				s.exec.Log(fmt.Sprintf("定时任务「%s」：GitHub 上传失败：%v", t.Name, err))
			} else {
				lines = append(lines, fmt.Sprintf("%s GitHub 上传 %d 条 → %s/%s",
					stamp(), n, cfg.GitHubRepo, cfg.GitHubPath))
			}
		}
		if t.Upload.Worker {
			s.exec.Log(fmt.Sprintf("定时任务「%s」：上报 Worker…", t.Name))
			uctx, uc := context.WithTimeout(context.Background(), 60*time.Second)
			n, err := s.uploadAPI(uctx, APITarget{Domain: cfg.WorkerDomain, UUID: cfg.UUID},
				rs, t.Upload.TopN, t.Upload.WorkerClear)
			uc()
			if err != nil {
				lines = append(lines, fmt.Sprintf("%s Worker 上报失败: %v", stamp(), err))
				s.exec.Log(fmt.Sprintf("定时任务「%s」：Worker 上报失败：%v", t.Name, err))
			} else {
				lines = append(lines, fmt.Sprintf("%s Worker 上报 %d 条 → %s", stamp(), n, cfg.WorkerDomain))
			}
		}
	}
	updateTask(t.ID, func(cur *ScheduleTask) {
		now := time.Now().Unix()
		cur.LastRun = now
		cur.NextRun = now + int64(cur.IntervalMin)*60
		cur.LastLog = appendLog(cur.LastLog, lines)
	})
}

// logTask 给任务记一行日志，并把下次执行时间往后推一个周期
func (s *Scheduler) logTask(t *ScheduleTask, msg string) {
	s.exec.Log(fmt.Sprintf("定时任务「%s」：%s", t.Name, msg))
	updateTask(t.ID, func(cur *ScheduleTask) {
		now := time.Now().Unix()
		cur.NextRun = now + int64(cur.IntervalMin)*60
		cur.LastLog = appendLog(cur.LastLog, []string{fmt.Sprintf("%s %s", stamp(), msg)})
	})
}

func uploadCount(topN, total int) int {
	if topN > 0 && topN < total {
		return topN
	}
	return total
}

func stamp() string { return time.Now().Format("01-02 15:04:05") }

// appendLog 追加日志行，超长时只留末尾
func appendLog(old string, lines []string) string {
	out := old
	for _, l := range lines {
		if out != "" {
			out += "\n"
		}
		out += l
	}
	const maxLog = 4000
	if len(out) > maxLog {
		out = out[len(out)-maxLog:]
	}
	return out
}

// genID 生成 8 位十六进制任务 ID
func genID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())[:8]
	}
	return hex.EncodeToString(b)
}
