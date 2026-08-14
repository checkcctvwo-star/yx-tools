package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeExec 模拟 Runner，捕获调度器发起的测速与日志，不碰真实网络
type fakeExec struct {
	running bool
	started []Options
	logs    []string
	// 模拟测速的结果与错误
	results []Result
	runErr  error
}

func (f *fakeExec) Running() bool { return f.running }

func (f *fakeExec) StartWithDone(o Options, onDone func([]Result, error)) bool {
	if f.running {
		return false
	}
	f.running = true
	f.started = append(f.started, o)
	go func() {
		time.Sleep(5 * time.Millisecond)
		f.running = false
		onDone(f.results, f.runErr)
	}()
	return true
}

func (f *fakeExec) Log(msg string) { f.logs = append(f.logs, msg) }

// resetConfigForTest 把配置读写重定向到临时目录，并清掉内存缓存
func resetConfigForTest(t *testing.T) {
	t.Helper()
	configDirOverride = filepath.Join(t.TempDir(), "cfg")
	if err := os.MkdirAll(configDirOverride, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgMu.Lock()
	cfg = nil
	cfgMu.Unlock()
}

func reloadConfigForTest(t *testing.T) *Config {
	t.Helper()
	cfgMu.Lock()
	cfg = nil
	cfgMu.Unlock()
	return LoadConfig()
}

func newDueTask() ScheduleTask {
	return ScheduleTask{
		ID:          "t1",
		Name:        "测试任务",
		Enabled:     true,
		IntervalMin: 60,
		Sources:     ScheduleSources{Text: "1.1.1.1\n2.2.2.2\n"},
		Upload:      ScheduleUpload{GitHub: true, Worker: true, TopN: 20, WorkerClear: true},
		Opts:        Options{Count: 10, Port: 443},
	}
}

// 撞车时跳过并记日志，同时把下次执行时间推后，避免每个 tick 都重试
func TestSchedulerSkipsWhenBusy(t *testing.T) {
	resetConfigForTest(t)
	fe := &fakeExec{running: true}
	s := NewScheduler(fe)

	c := LoadConfig()
	task := newDueTask()
	c.Schedules = append(c.Schedules, task)
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}

	s.runOne(&task)

	if len(fe.started) != 0 {
		t.Fatal("正在跑别的任务时不应再启动测速")
	}
	got := reloadConfigForTest(t)
	if len(got.Schedules) != 1 {
		t.Fatal("任务应还在")
	}
	g := got.Schedules[0]
	if g.LastRun != 0 {
		t.Error("跳过的轮次不应记 LastRun")
	}
	if g.NextRun == 0 {
		t.Error("跳过后应把 NextRun 推后")
	}
	if !strings.Contains(g.LastLog, "跳过") {
		t.Errorf("日志应记录跳过: %q", g.LastLog)
	}
}

// 正常跑完：候选写入、测速启动、GitHub 与 Worker 各自上报、日志与下次执行时间落盘
func TestSchedulerRunAndUploads(t *testing.T) {
	resetConfigForTest(t)
	fe := &fakeExec{results: makeResults(25)}
	var ghRS []Result
	var ghTopN int
	var apiRS []Result
	var apiClear bool
	s := NewScheduler(fe)
	s.uploadGitHub = func(_ context.Context, t GitHubTarget, rs []Result, limit int) (int, error) {
		ghRS, ghTopN = rs, limit
		return 2, nil
	}
	s.uploadAPI = func(_ context.Context, t APITarget, rs []Result, limit int, clear bool) (int, error) {
		apiRS, apiClear = rs, clear
		return 3, nil
	}

	c := LoadConfig()
	task := newDueTask()
	c.Schedules = append(c.Schedules, task)
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}

	s.runOne(&task)

	if len(fe.started) != 1 {
		t.Fatalf("应启动一次测速，got %d", len(fe.started))
	}
	if !fe.started[0].Proxy {
		t.Error("定时测速应以反代模式跑合并列表")
	}
	if len(ghRS) != 25 || ghTopN != 20 {
		t.Errorf("GitHub 上报应收到全部结果与前 N 限制: %d/%d", len(ghRS), ghTopN)
	}
	if len(apiRS) != 25 || !apiClear {
		t.Errorf("Worker 上报应收到全部结果且带清空标记: %d/%v", len(apiRS), apiClear)
	}

	got := reloadConfigForTest(t)
	g := got.Schedules[0]
	if g.LastRun == 0 {
		t.Error("跑完应记录 LastRun")
	}
	if g.NextRun <= g.LastRun {
		t.Error("NextRun 应在 LastRun 之后")
	}
	for _, want := range []string{"测速完成", "前 20 个上报", "GitHub 上传 2 条", "Worker 上报 3 条"} {
		if !strings.Contains(g.LastLog, want) {
			t.Errorf("日志应包含 %q: %q", want, g.LastLog)
		}
	}
}

// 来源加载失败：不启动测速，记日志并推后下次执行
func TestSchedulerSourceFailureLogs(t *testing.T) {
	resetConfigForTest(t)
	fe := &fakeExec{}
	s := NewScheduler(fe)

	c := LoadConfig()
	task := newDueTask()
	task.Sources = ScheduleSources{} // 空来源
	c.Schedules = append(c.Schedules, task)
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}

	s.runOne(&task)

	if len(fe.started) != 0 {
		t.Fatal("来源为空不应启动测速")
	}
	got := reloadConfigForTest(t)
	if !strings.Contains(got.Schedules[0].LastLog, "候选来源加载失败") {
		t.Errorf("应记录来源失败: %q", got.Schedules[0].LastLog)
	}
}

// CheckDue 只处理启用且到期的任务
func TestCheckDueOnlyDueEnabled(t *testing.T) {
	resetConfigForTest(t)
	fe := &fakeExec{running: true} // 忙，让到期任务走跳过分支
	s := NewScheduler(fe)

	now := time.Now().Unix()
	c := LoadConfig()
	due := newDueTask()
	due.ID = "due"
	future := newDueTask()
	future.ID = "future"
	future.NextRun = now + 3600
	off := newDueTask()
	off.ID = "off"
	off.Enabled = false
	c.Schedules = []ScheduleTask{due, future, off}
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}

	s.CheckDue()

	got := reloadConfigForTest(t)
	byID := map[string]ScheduleTask{}
	for _, x := range got.Schedules {
		byID[x.ID] = x
	}
	if !strings.Contains(byID["due"].LastLog, "跳过") {
		t.Error("到期任务应被执行（并因忙而跳过）")
	}
	if byID["future"].LastLog != "" || byID["future"].NextRun != now+3600 {
		t.Error("未到期任务不应被动")
	}
	if byID["off"].LastLog != "" {
		t.Error("停用任务不应被动")
	}
}

// RunNow 找不到任务要报错
func TestRunNowMissing(t *testing.T) {
	resetConfigForTest(t)
	s := NewScheduler(&fakeExec{})
	if err := s.RunNow("nope"); err == nil {
		t.Fatal("不存在的任务应报错")
	}
}

// 配置归一化：旧配置没 schedules 不报错；任务字段补缺省
func TestLoadConfigWithSchedules(t *testing.T) {
	resetConfigForTest(t)
	body := `{
		"worker_domain": "a.workers.dev",
		"schedules": [
			{"id": "x1", "enabled": true, "upload": {"top_n": -5}, "sources": {"random_cf": true, "random_cf_count": 0}},
			{"id": "x2", "interval_minutes": 0, "name": "  "}
		]
	}`
	path := ConfigPath()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := loadConfigFrom(path)
	if c.WorkerDomain != "a.workers.dev" {
		t.Error("旧字段应照常解析")
	}
	if len(c.Schedules) != 2 {
		t.Fatalf("want 2 schedules, got %d", len(c.Schedules))
	}
	if c.Schedules[0].Upload.TopN != 0 {
		t.Error("负数 topN 应归零")
	}
	if c.Schedules[0].Sources.RandomCFCount != 100 {
		t.Error("随机 CF 数量应补 100")
	}
	if c.Schedules[1].IntervalMin != 360 {
		t.Error("频率缺省应补 6 小时")
	}
	if c.Schedules[1].Name == "" {
		t.Error("名字应自动补")
	}
}
