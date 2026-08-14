package app

import "testing"

// 回放只取最近的 64 条：历史超过通道容量时，末尾的终态事件必须仍在，
// 否则界面重连后会错过「完成」而永远卡在运行态。
func TestSubscribeReplayTailContainsDone(t *testing.T) {
	r := NewRunner()
	// 灌 200 条进度事件 + 1 条终态，超过通道容量 64
	for i := 0; i < 200; i++ {
		r.broadcast(Event{Type: "progress", Message: "tick", Current: i, Total: 200})
	}
	r.broadcast(Event{Type: "done", Message: "完成", Finished: true, Results: []Result{{IP: "1.1.1.1"}}})

	ch, unsub := r.Subscribe()
	defer unsub()

	var got []Event
	for i := 0; i < 64; i++ {
		select {
		case e := <-ch:
			got = append(got, e)
		default:
			t.Fatalf("第 %d 次读取应拿到回放事件（回放应恰好 64 条）", i)
		}
	}
	if len(got) != 64 {
		t.Fatalf("回放应恰好 64 条，got %d", len(got))
	}
	last := got[len(got)-1]
	if last.Type != "done" || !last.Finished {
		t.Fatalf("回放末尾应为终态 done 事件，got %+v", last)
	}
}
