package main

import (
	"encoding/json"
	"testing"
	"time"
)

// 테스트용 허브: run() 고루틴 없이 핸들러를 직접 부르고, 시계를 손으로 돌린다.
type testHub struct {
	*Hub
	clock time.Time
	saved int
}

func newTestHub(t *testing.T) *testHub {
	th := &testHub{Hub: newHub(), clock: time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)}
	th.now = func() time.Time { return th.clock }
	th.save = func(*CourseFile) { th.saved++ }
	th.onHostMessage(msgIn{T: "newCourse", Name: "테스트", Length: 600, Diff: "easy", Limit: 2})
	if th.course == nil {
		t.Fatal("코스가 안 만들어짐")
	}
	return th
}

func (th *testHub) join(sid, name string) *client {
	c := &client{hub: th.Hub, send: make(chan []byte, 256), token: newID(), sid: sid, name: name}
	th.onRegister(c)
	return c
}

func (th *testHub) msg(c *client, v any) {
	b, _ := json.Marshal(v)
	th.onMessage(inMsg{c: c, data: b})
}

// last 는 c 가 받은 메시지 중 t 종류의 마지막 것.
func last(c *client, t string) map[string]any {
	var got map[string]any
	for {
		select {
		case b := <-c.send:
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if m["t"] == t {
				got = m
			}
		default:
			return got
		}
	}
}

func TestRunFlow(t *testing.T) {
	th := newTestHub(t)
	a := th.join("10101", "가")

	// 기록 전: 공식 도전 불가, 연습은 가능(기록 안 됨)
	th.msg(a, map[string]any{"t": "start"})
	if m := last(a, "err"); m == nil {
		t.Fatal("기록 시작 전 공식 도전이 허용됨")
	}
	th.msg(a, map[string]any{"t": "start", "practice": true})
	g := last(a, "go")
	if g == nil {
		t.Fatal("연습 시작 안 됨")
	}
	th.clock = th.clock.Add(2 * time.Minute)
	th.msg(a, map[string]any{"t": "end", "run": g["run"], "toggles": []int{}})
	if r := last(a, "result"); r == nil || r["practice"] != true {
		t.Fatalf("연습 결과 이상: %v", r)
	}
	if len(th.cfile.Runs) != 0 {
		t.Fatal("연습이 기록됨")
	}

	// 기록 시작 → 자동 조종 입력으로 완주
	th.onHostMessage(msgIn{T: "setOpen", Open: true})
	tg, ok := autopilot(th.course)
	if !ok {
		t.Fatal("자동 조종 실패")
	}
	th.msg(a, map[string]any{"t": "start"})
	g = last(a, "go")
	th.clock = th.clock.Add(70 * time.Second)
	th.msg(a, map[string]any{"t": "end", "run": g["run"], "toggles": tg})
	r := last(a, "result")
	if r == nil || r["finished"] != true || r["rank"] != float64(1) || r["newBest"] != true {
		t.Fatalf("완주 결과 이상: %v", r)
	}
	want := replay(th.course, tg).Score()
	if int(r["score"].(float64)) != want {
		t.Fatalf("점수 %v, 서버 재계산 %d", r["score"], want)
	}

	// 두 번째 도전: 너무 빨리 끝남(계산만 돌려 보내기) → 거부, 그래도 횟수는 쓴다
	th.msg(a, map[string]any{"t": "start"})
	g = last(a, "go")
	th.clock = th.clock.Add(3 * time.Second)
	th.msg(a, map[string]any{"t": "end", "run": g["run"], "toggles": tg})
	if r := last(a, "result"); r == nil || r["void"] != true {
		t.Fatalf("너무 빠른 주행이 받아들여짐: %v", r)
	}
	// 제한 2회 → 세 번째 출발 불가
	th.msg(a, map[string]any{"t": "start"})
	if last(a, "go") != nil {
		t.Fatal("도전 횟수 제한이 안 걸림")
	}

	// 다른 학생: 아무것도 안 누르고 침몰
	b := th.join("10102", "나")
	th.msg(b, map[string]any{"t": "start"})
	g = last(b, "go")
	th.clock = th.clock.Add(70 * time.Second)
	th.msg(b, map[string]any{"t": "end", "run": g["run"], "toggles": []int{}})
	rb := last(b, "result")
	if rb["finished"] != false || rb["rank"] != float64(2) {
		t.Fatalf("침몰 결과 이상: %v", rb)
	}
	board := th.leaderboard()
	if len(board) != 2 || board[0].Name != "가" || board[0].Tries != 2 {
		t.Fatalf("순위표 이상: %+v", board)
	}
}

func TestBadToggles(t *testing.T) {
	th := newTestHub(t)
	th.onHostMessage(msgIn{T: "setOpen", Open: true})
	a := th.join("1", "가")
	th.msg(a, map[string]any{"t": "start"})
	g := last(a, "go")
	th.clock = th.clock.Add(2 * time.Minute)
	th.msg(a, map[string]any{"t": "end", "run": g["run"], "toggles": []int{5, 5, 9}})
	if r := last(a, "result"); r == nil || r["void"] != true {
		t.Fatalf("중복 토글이 받아들여짐: %v", r)
	}
}

func TestReconnectKeepsRun(t *testing.T) {
	th := newTestHub(t)
	th.onHostMessage(msgIn{T: "setOpen", Open: true})
	a := th.join("7", "다")
	th.msg(a, map[string]any{"t": "start"})
	g := last(a, "go")
	th.onUnregister(a)
	// 브라우저를 닫고 새 토큰으로 들어와도 같은 학번·이름이면 그 주행을 이어받는다
	a2 := th.join("7", "다")
	if last(a2, "resume") == nil {
		t.Fatal("재접속 시 주행 복귀 안내가 없음")
	}
	th.clock = th.clock.Add(2 * time.Minute)
	th.msg(a2, map[string]any{"t": "end", "run": g["run"], "toggles": []int{}})
	if r := last(a2, "result"); r == nil || r["void"] == true {
		t.Fatalf("재접속 후 결과 이상: %v", r)
	}
}

func TestNewCourseCancelsRuns(t *testing.T) {
	th := newTestHub(t)
	a := th.join("1", "가")
	th.msg(a, map[string]any{"t": "start", "practice": true})
	g := last(a, "go")
	th.onHostMessage(msgIn{T: "newCourse", Name: "둘째", Length: 900, Diff: "hard", Limit: 0})
	th.clock = th.clock.Add(2 * time.Minute)
	th.msg(a, map[string]any{"t": "end", "run": g["run"], "toggles": []int{}})
	if r := last(a, "result"); r == nil || r["void"] != true {
		t.Fatalf("이전 코스 주행이 받아들여짐: %v", r)
	}
}

func TestBoardTies(t *testing.T) {
	runs := []RunRecord{
		{SID: "1", Name: "가", Score: 500, At: "2026-09-28T10:00:00+09:00"},
		{SID: "2", Name: "나", Score: 700, At: "2026-09-28T10:01:00+09:00"},
		{SID: "3", Name: "다", Score: 500, At: "2026-09-28T10:02:00+09:00"},
		{SID: "1", Name: "가", Score: 300, At: "2026-09-28T10:03:00+09:00"},
	}
	b := buildBoard(runs, nil)
	if len(b) != 3 || b[0].Name != "나" || b[1].Rank != 2 || b[2].Rank != 2 || b[1].Name != "가" || b[1].Runs != 2 {
		t.Fatalf("순위 이상: %+v", b)
	}
}

func TestHideCourse(t *testing.T) {
	th := newTestHub(t)
	th.onHostMessage(msgIn{T: "setOpen", Open: true})
	a := th.join("1", "가")
	th.msg(a, map[string]any{"t": "start"})
	g := last(a, "go")
	if g == nil {
		t.Fatal("출발 안 됨")
	}

	// 내리기: 달리던 주행은 무효, 기록도 닫힘, 학생에게 코스가 사라짐
	th.onHostMessage(msgIn{T: "setShown", Shown: false})
	if th.course.Open || th.shown || len(th.runs) != 0 {
		t.Fatalf("내리기 후 상태 이상: open=%v shown=%v runs=%d", th.course.Open, th.shown, len(th.runs))
	}
	th.onTick()
	if m := last(a, "course"); m == nil || m["course"] != nil {
		t.Fatalf("학생에게 코스가 그대로 보임: %v", m)
	}
	th.clock = th.clock.Add(2 * time.Minute)
	th.msg(a, map[string]any{"t": "end", "run": g["run"], "toggles": []int{}})
	if r := last(a, "result"); r == nil || r["void"] != true {
		t.Fatalf("내린 뒤 끝난 주행이 기록됨: %v", r)
	}
	for _, practice := range []bool{false, true} {
		th.msg(a, map[string]any{"t": "start", "practice": practice})
		if last(a, "go") != nil {
			t.Fatalf("내려 둔 코스로 출발됨(연습=%v)", practice)
		}
	}
	th.onHostMessage(msgIn{T: "setOpen", Open: true})
	if th.course.Open {
		t.Fatal("내려 둔 코스에 기록 시작이 됨")
	}

	// 올리기: 연습 가능
	th.onHostMessage(msgIn{T: "setShown", Shown: true})
	th.onTick()
	if m := last(a, "course"); m == nil || m["course"] == nil {
		t.Fatal("올렸는데 학생에게 코스가 안 옴")
	}
	th.msg(a, map[string]any{"t": "start", "practice": true})
	if last(a, "go") == nil {
		t.Fatal("올린 뒤 연습이 안 됨")
	}
}

func TestRestoreStartsHidden(t *testing.T) {
	h := newHub()
	h.save = func(*CourseFile) {}
	cf := &CourseFile{Course: Course{ID: "c1", Name: "지난 코스", Seed: 7, Length: 600, Diff: "easy", Open: true}}
	cf.Course.Open = false // main.go 와 같이: 다시 켜면 기록은 닫고
	h.setCourse(cf, false) // 내려 둔 채로
	c := &client{hub: h, send: make(chan []byte, 64), token: "t", name: "가"}
	h.onRegister(c)
	if m := last(c, "course"); m == nil || m["course"] != nil {
		t.Fatalf("서버를 다시 켜자마자 학생에게 코스가 보임: %v", m)
	}
}
