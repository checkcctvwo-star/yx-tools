package task

import "testing"

// 下载队列长度的四种组合：全部测完 / 设了速度下限 / 候选不足 / 默认凑够即停
func TestDownloadQueue(t *testing.T) {
	cases := []struct {
		name      string
		total     int
		testCount int
		minSpeed  float64
		all       bool
		want      int
	}{
		{"凑够即停：候选足量，只测前 N", 100, 10, 0, false, 10},
		{"候选不足就全测", 5, 10, 0, false, 5},
		{"设了速度下限就全测（直到凑够）", 100, 10, 1.0, false, 100},
		{"全部测完：候选有多少测多少", 100, 10, 0, true, 100},
		{"全部测完：带速度下限也是全测", 100, 10, 1.0, true, 100},
		{"全部测完：候选不足", 5, 10, 0, true, 5},
	}
	for _, c := range cases {
		if got := downloadQueue(c.total, c.testCount, c.minSpeed, c.all); got != c.want {
			t.Errorf("%s: downloadQueue(%d,%d,%v,%v)=%d, want %d",
				c.name, c.total, c.testCount, c.minSpeed, c.all, got, c.want)
		}
	}
}
