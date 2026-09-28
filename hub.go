package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// 연결·주행·순위 레이어. 모든 상태 변경은 run() 단일 고루틴 안에서만 일어나므로
// 락이 필요 없다(액터 모델 — vector-soccer·classroom-quiz 와 같은 구조).
//
// 주행 흐름:  학생 "start" → 서버 "go"(주행 id) → 학생 화면에서 물리 진행(0.25초마다 "pos" 로 위치 보고)
//            → 끝나면 "end"(버튼 토글 틱 목록) → 서버가 replay 로 다시 계산해 점수 확정 → "result".

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10

	tickPeriod    = time.Second / 10
	ghostEvery    = 2 // 10Hz 틱 중 2틱마다 = 5Hz 로 다른 학생 잠수함 위치 전송
	hostEvery     = 2 // 교사 화면 5Hz
	offlineForget = 15 * time.Minute
	runMaxAge     = 30 * time.Minute
	maxToggles    = 40000
	topN          = 10
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true }, // 로컬 교실 전용
}

type client struct {
	hub   *Hub
	conn  *websocket.Conn
	send  chan []byte
	host  bool
	token string
	sid   string
	name  string
}

type inMsg struct {
	c    *client
	data []byte
}

// Player 는 학생 한 명. 연결(client)과 분리되어 있어 재접속하면 conn 만 갈아낀다.
type Player struct {
	id       string
	token    string
	sid      string
	name     string
	conn     *client
	offSince time.Time
	run      *Run
}

func (p *Player) key() string { return playerKey(p.sid, p.name) }

func playerKey(sid, name string) string { return sid + "|" + name }

// Run 은 진행 중인 주행 하나.
type Run struct {
	id       string
	p        *Player
	course   string
	practice bool
	started  time.Time
	x, y     float64
	hp, sc   int
	at       time.Time // 마지막 위치 보고
}

// RunRecord 는 끝난 공식 주행 하나(코스 파일에 쌓인다).
type RunRecord struct {
	SID      string `json:"sid"`
	Name     string `json:"name"`
	Try      int    `json:"try"` // 그 학생의 몇 번째 도전
	Score    int    `json:"score"`
	Dist     int    `json:"dist"`
	Pearls   int    `json:"pearls"`
	HP       int    `json:"hp"`
	Finished bool   `json:"finished"`
	Hits     int    `json:"hits"`
	Ticks    int    `json:"ticks"`
	At       string `json:"at"` // RFC3339
}

func (r *RunRecord) key() string { return playerKey(r.SID, r.Name) }

type Hub struct {
	register   chan *client
	unregister chan *client
	inbound    chan inMsg

	hosts   map[*client]bool
	players map[string]*Player // 토큰 → 학생
	runs    map[string]*Run

	course  *Course
	cfile   *CourseFile // 저장되는 기록(course 메타 + 주행 기록 + 도전 횟수)
	board   []BoardRow  // records 가 바뀔 때만 다시 계산
	boardOK bool

	lobbyDirty  bool
	courseDirty bool // 학생·교사에게 코스를 다시 보내야 함
	tick        int
	now         func() time.Time
	save        func(*CourseFile)
}

func newHub() *Hub {
	return &Hub{
		register:   make(chan *client),
		unregister: make(chan *client),
		inbound:    make(chan inMsg, 256),
		hosts:      map[*client]bool{},
		players:    map[string]*Player{},
		runs:       map[string]*Run{},
		now:        time.Now,
		save:       saveCourseFile,
	}
}

// setCourse 는 코스를 바꾼다(새로 만들기 또는 서버 시작 시 복원). 진행 중이던 주행은 모두 무효.
func (h *Hub) setCourse(cf *CourseFile) {
	c := cf.Course
	c.build()
	h.course = &c
	h.cfile = cf
	currentCourse.Store(cf.Date + "/" + cf.File)
	if h.cfile.Tries == nil {
		h.cfile.Tries = map[string]int{}
	}
	for id, r := range h.runs {
		r.p.run = nil
		delete(h.runs, id)
	}
	h.boardOK = false
	h.courseDirty = true
	h.lobbyDirty = true
}

func (h *Hub) run() {
	t := time.NewTicker(tickPeriod)
	defer t.Stop()
	for {
		select {
		case c := <-h.register:
			h.onRegister(c)
		case c := <-h.unregister:
			h.onUnregister(c)
		case m := <-h.inbound:
			h.onMessage(m)
		case <-t.C:
			h.onTick()
		}
	}
}

// ── 연결 ────────────────────────────────────────────────────

func (h *Hub) onRegister(c *client) {
	if c.host {
		h.hosts[c] = true
		h.sendTo(c, h.hostCourseMsg())
		h.sendTo(c, h.hostMsg())
		return
	}
	p, ok := h.players[c.token]
	if ok {
		if p.conn != nil && p.conn != c {
			close(p.conn.send)
		}
		p.sid, p.name = c.sid, c.name
	} else if old := h.offlineByKey(playerKey(c.sid, c.name)); old != nil {
		// 브라우저를 닫았다 새로 들어옴 — 같은 학번·이름이면 그 학생(진행 중 주행 포함)으로 복귀
		delete(h.players, old.token)
		old.token = c.token
		h.players[c.token] = old
		p = old
	} else {
		p = &Player{id: newID(), token: c.token, sid: c.sid, name: c.name}
		h.players[c.token] = p
		log.Printf("학생 입장: %s — 총 %d명", label(p.sid, p.name), len(h.players))
	}
	p.conn = c
	p.offSince = time.Time{}
	h.sendTo(c, mustJSON(map[string]any{"t": "hello", "id": p.id, "name": p.name, "sid": p.sid}))
	if h.course != nil {
		h.sendTo(c, h.courseMsg())
	}
	h.sendTo(c, h.statusMsg(p))
	if p.run != nil {
		h.sendTo(c, mustJSON(map[string]any{"t": "resume", "run": p.run.id, "course": p.run.course, "practice": p.run.practice}))
	}
	h.lobbyDirty = true
}

func (h *Hub) offlineByKey(key string) *Player {
	for _, p := range h.players {
		if p.conn == nil && p.key() == key {
			return p
		}
	}
	return nil
}

func (h *Hub) onUnregister(c *client) {
	if c.host {
		if h.hosts[c] {
			delete(h.hosts, c)
			close(c.send)
		}
		return
	}
	p, ok := h.players[c.token]
	if !ok || p.conn != c {
		return // 이미 새 연결로 갈아낀 이전 연결
	}
	close(c.send)
	p.conn = nil
	p.offSince = h.now()
	h.lobbyDirty = true
}

func (h *Hub) sendTo(c *client, b []byte) {
	if c == nil {
		return
	}
	select {
	case c.send <- b:
	default: // 느린 클라이언트 — 이번 메시지는 버린다(다음 갱신이 곧 온다)
	}
}

func (h *Hub) sendP(p *Player, v any) {
	if p.conn != nil {
		h.sendTo(p.conn, mustJSON(v))
	}
}

func (h *Hub) sendErr(p *Player, msg string) {
	h.sendP(p, map[string]any{"t": "err", "msg": msg})
}

// ── 메시지 ──────────────────────────────────────────────────

type msgIn struct {
	T        string  `json:"t"`
	Practice bool    `json:"practice"`
	Run      string  `json:"run"`
	Toggles  []int   `json:"toggles"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	HP       int     `json:"hp"`
	SC       int     `json:"sc"`

	// 교사
	Name   string `json:"name"`
	Length int    `json:"length"`
	Diff   string `json:"diff"`
	Limit  int    `json:"limit"`
	Open   bool   `json:"open"`
}

func (h *Hub) onMessage(in inMsg) {
	var m msgIn
	if json.Unmarshal(in.data, &m) != nil {
		return
	}
	if in.c.host {
		h.onHostMessage(m)
		return
	}
	p, ok := h.players[in.c.token]
	if !ok || p.conn != in.c {
		return
	}
	switch m.T {
	case "start":
		h.startRun(p, m.Practice)
	case "pos":
		if r := p.run; r != nil && r.id == m.Run {
			r.x, r.y, r.hp, r.sc, r.at = m.X, m.Y, m.HP, m.SC, h.now()
		}
	case "end":
		h.endRun(p, m.Run, m.Toggles)
	case "quit":
		if r := p.run; r != nil && r.id == m.Run {
			delete(h.runs, r.id)
			p.run = nil
			h.lobbyDirty = true
		}
	}
}

func (h *Hub) startRun(p *Player, practice bool) {
	if h.course == nil {
		h.sendErr(p, "아직 코스가 없어요. 선생님을 기다려 주세요.")
		return
	}
	if !practice {
		if !h.course.Open {
			h.sendErr(p, "아직 기록이 시작되지 않았어요. 연습 주행은 할 수 있어요.")
			return
		}
		if lim := h.course.Limit; lim > 0 && h.cfile.Tries[p.key()] >= lim {
			h.sendErr(p, "도전 횟수를 다 썼어요.")
			return
		}
	} else if h.course.Open {
		h.sendErr(p, "기록 중에는 연습 주행을 할 수 없어요.")
		return
	}
	if old := p.run; old != nil {
		delete(h.runs, old.id)
	}
	now := h.now()
	r := &Run{id: newID(), p: p, course: h.course.ID, practice: practice, started: now, at: now,
		y: simStartY, hp: simHP}
	p.run = r
	h.runs[r.id] = r
	if !practice {
		h.cfile.Tries[p.key()]++ // 출발할 때 센다(중간에 그만둬도 1회)
		h.save(h.cfile)
	}
	h.sendP(p, map[string]any{"t": "go", "run": r.id, "course": r.course, "practice": practice})
	h.lobbyDirty = true
}

func (h *Hub) endRun(p *Player, runID string, toggles []int) {
	r := p.run
	if r == nil || r.id != runID {
		h.sendP(p, map[string]any{"t": "result", "run": runID, "void": true, "msg": "이 주행은 기록할 수 없어요(코스가 바뀌었거나 이미 끝남)."})
		return
	}
	delete(h.runs, r.id)
	p.run = nil
	h.lobbyDirty = true

	if len(toggles) > maxToggles {
		h.sendP(p, map[string]any{"t": "result", "run": runID, "void": true, "msg": "입력이 너무 많아요."})
		return
	}
	for i, tk := range toggles {
		if tk < 0 || (i > 0 && tk <= toggles[i-1]) {
			h.sendP(p, map[string]any{"t": "result", "run": runID, "void": true, "msg": "입력 기록이 올바르지 않아요."})
			return
		}
	}
	s := replay(h.course, toggles)
	// 실제로 흐른 시간보다 빨리 끝날 수는 없다(화면 없이 계산만 돌려 보내는 조작 방지)
	if el := h.now().Sub(r.started).Seconds(); el < float64(s.Tick)/60*0.85-1 {
		log.Printf("주행 거부(시간 부족): %s %.1fs < %.1fs", label(p.sid, p.name), el, float64(s.Tick)/60)
		h.sendP(p, map[string]any{"t": "result", "run": runID, "void": true, "msg": "주행 시간이 맞지 않아 기록하지 않았어요."})
		return
	}
	rec := RunRecord{SID: p.sid, Name: p.name, Score: s.Score(), Dist: s.Dist(), Pearls: s.Pearls, HP: s.HP,
		Finished: s.Done == "finish", Hits: s.Hits, Ticks: s.Tick, At: h.now().Format(time.RFC3339)}
	res := map[string]any{"t": "result", "run": runID, "practice": r.practice, "score": rec.Score, "dist": rec.Dist,
		"pearls": rec.Pearls, "hp": rec.HP, "finished": rec.Finished}
	if !r.practice {
		prevBest := h.bestOf(p.key())
		rec.Try = h.cfile.Tries[p.key()]
		h.cfile.Runs = append(h.cfile.Runs, rec)
		h.boardOK = false
		h.save(h.cfile)
		res["newBest"] = prevBest == nil || rec.Score > prevBest.Score
		if row := h.rowOf(p.key()); row != nil {
			res["rank"], res["total"], res["best"] = row.Rank, len(h.leaderboard()), row.Score
		}
		log.Printf("기록: %s %d점 (%dm, 진주 %d, %s)", label(p.sid, p.name), rec.Score, rec.Dist, rec.Pearls,
			map[bool]string{true: "완주", false: "침몰"}[rec.Finished])
	}
	h.sendP(p, res)
}

func (h *Hub) onHostMessage(m msgIn) {
	switch m.T {
	case "newCourse":
		if !validDiff(m.Diff) || m.Length < 300 || m.Length > 3000 || m.Limit < 0 || m.Limit > 99 {
			return
		}
		name := cleanText(m.Name, 30)
		if name == "" {
			name = "잠수함 코스"
		}
		now := h.now()
		cf := &CourseFile{Course: Course{ID: newID(), Name: name, Seed: randSeed(), Length: m.Length, Diff: m.Diff,
			CreatedAt: now.Format(time.RFC3339), Limit: m.Limit}, Tries: map[string]int{}, Runs: []RunRecord{}}
		cf.Date = now.Format("2006-01-02")
		cf.File = now.Format("150405") + "-" + cf.Course.ID + ".json"
		h.setCourse(cf)
		h.save(h.cfile)
		log.Printf("새 코스: %s (%dm · %s · 도전 %d회)", name, m.Length, diffKo[m.Diff], m.Limit)
	case "setOpen":
		if h.course == nil {
			return
		}
		h.course.Open = m.Open
		h.cfile.Course.Open = m.Open
		h.save(h.cfile)
		h.courseDirty = true
		h.lobbyDirty = true
	case "setLimit":
		if h.course == nil || m.Limit < 0 || m.Limit > 99 {
			return
		}
		h.course.Limit = m.Limit
		h.cfile.Course.Limit = m.Limit
		h.save(h.cfile)
		h.courseDirty = true
		h.lobbyDirty = true
	}
}

// ── 순위 ────────────────────────────────────────────────────

type BoardRow struct {
	Rank     int    `json:"rank"`
	SID      string `json:"sid"`
	Name     string `json:"name"`
	Score    int    `json:"score"`
	Dist     int    `json:"dist"`
	Pearls   int    `json:"pearls"`
	HP       int    `json:"hp"`
	Finished bool   `json:"fin"`
	Tries    int    `json:"tries"`
	Runs     int    `json:"runs"`
	At       string `json:"at"`
}

// buildBoard 는 학생별 최고 기록 순위표. 점수 → 먼저 세운 기록 순. 같은 점수는 같은 등수.
func buildBoard(runs []RunRecord, tries map[string]int) []BoardRow {
	best := map[string]*RunRecord{}
	count := map[string]int{}
	for i := range runs {
		r := &runs[i]
		k := r.key()
		count[k]++
		if b, ok := best[k]; !ok || r.Score > b.Score {
			best[k] = r
		}
	}
	rows := make([]BoardRow, 0, len(best))
	for k, r := range best {
		t := tries[k]
		if t < count[k] {
			t = count[k]
		}
		rows = append(rows, BoardRow{SID: r.SID, Name: r.Name, Score: r.Score, Dist: r.Dist, Pearls: r.Pearls,
			HP: r.HP, Finished: r.Finished, Tries: t, Runs: count[k], At: r.At})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		return rows[i].At < rows[j].At
	})
	for i := range rows {
		if i > 0 && rows[i].Score == rows[i-1].Score {
			rows[i].Rank = rows[i-1].Rank
		} else {
			rows[i].Rank = i + 1
		}
	}
	return rows
}

func (h *Hub) leaderboard() []BoardRow {
	if h.cfile == nil {
		return []BoardRow{}
	}
	if !h.boardOK {
		h.board = buildBoard(h.cfile.Runs, h.cfile.Tries)
		h.boardOK = true
	}
	return h.board
}

func (h *Hub) rowOf(key string) *BoardRow {
	for i, r := range h.leaderboard() {
		if playerKey(r.SID, r.Name) == key {
			return &h.leaderboard()[i]
		}
	}
	return nil
}

func (h *Hub) bestOf(key string) *BoardRow { return h.rowOf(key) }

// ── 주기 작업 ────────────────────────────────────────────────

func (h *Hub) onTick() {
	h.tick++
	now := h.now()

	for id, r := range h.runs {
		if now.Sub(r.started) > runMaxAge {
			r.p.run = nil
			delete(h.runs, id)
		}
	}
	for tok, p := range h.players {
		if p.conn == nil && p.run == nil && now.Sub(p.offSince) > offlineForget {
			delete(h.players, tok)
			h.lobbyDirty = true
		}
	}

	if h.courseDirty {
		h.courseDirty = false
		b := h.courseMsg()
		for _, p := range h.players {
			h.sendP2(p, b)
		}
		hb := h.hostCourseMsg()
		for c := range h.hosts {
			h.sendTo(c, hb)
		}
	}
	if h.lobbyDirty {
		h.lobbyDirty = false
		for _, p := range h.players {
			if p.conn != nil {
				h.sendTo(p.conn, h.statusMsg(p))
			}
		}
	}
	if h.tick%ghostEvery == 0 && len(h.runs) > 1 {
		h.broadcastGhosts(now)
	}
	if h.tick%hostEvery == 0 && len(h.hosts) > 0 {
		b := h.hostMsg()
		for c := range h.hosts {
			h.sendTo(c, b)
		}
	}
}

func (h *Hub) sendP2(p *Player, b []byte) {
	if p.conn != nil {
		h.sendTo(p.conn, b)
	}
}

func r1(x float64) float64 { return math.Round(x*10) / 10 }

// broadcastGhosts 는 주행 중인 학생에게 다른 학생 잠수함 위치(반투명 "유령")를 보낸다.
func (h *Hub) broadcastGhosts(now time.Time) {
	type ghost struct {
		ID   string  `json:"id"`
		Name string  `json:"n"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
	}
	all := make([]ghost, 0, len(h.runs))
	for _, r := range h.runs {
		if now.Sub(r.at) < 2*time.Second && r.course == h.course.ID {
			all = append(all, ghost{r.p.id, r.p.name, r1(r.x), r1(r.y)})
		}
	}
	for _, r := range h.runs {
		if r.p.conn == nil {
			continue
		}
		list := make([]ghost, 0, len(all))
		for _, g := range all {
			if g.ID != r.p.id {
				list = append(list, g)
			}
		}
		h.sendP(r.p, map[string]any{"t": "ghosts", "g": list})
	}
}

// ── 보내는 메시지 ────────────────────────────────────────────

func (h *Hub) courseMeta() map[string]any {
	c := h.course
	return map[string]any{"id": c.ID, "name": c.Name, "length": c.Length, "diff": c.Diff,
		"open": c.Open, "limit": c.Limit, "createdAt": c.CreatedAt}
}

func (h *Hub) courseMsgAs(t string) []byte {
	if h.course == nil {
		return mustJSON(map[string]any{"t": t, "course": nil})
	}
	m := h.courseMeta()
	m["obs"], m["pearls"] = h.course.Obs, h.course.Pearls
	return mustJSON(map[string]any{"t": t, "course": m})
}

func (h *Hub) courseMsg() []byte     { return h.courseMsgAs("course") }
func (h *Hub) hostCourseMsg() []byte { return h.courseMsgAs("hcourse") }

func (h *Hub) statusMsg(p *Player) []byte {
	m := map[string]any{"t": "status"}
	if h.course == nil {
		return mustJSON(m)
	}
	board := h.leaderboard()
	top := board
	if len(top) > topN {
		top = top[:topN]
	}
	type trow struct {
		Rank  int    `json:"rank"`
		Name  string `json:"name"`
		Score int    `json:"score"`
		Fin   bool   `json:"fin"`
		Me    bool   `json:"me"`
	}
	rows := make([]trow, 0, len(top))
	for _, r := range top {
		rows = append(rows, trow{r.Rank, r.Name, r.Score, r.Finished, playerKey(r.SID, r.Name) == p.key()})
	}
	m["open"], m["limit"] = h.course.Open, h.course.Limit
	m["tries"] = h.cfile.Tries[p.key()]
	m["top"], m["total"] = rows, len(board)
	if me := h.rowOf(p.key()); me != nil {
		m["me"] = me
	}
	return mustJSON(m)
}

func (h *Hub) hostMsg() []byte {
	now := h.now()
	type prow struct {
		ID     string `json:"id"`
		SID    string `json:"sid"`
		Name   string `json:"name"`
		Online bool   `json:"online"`
		State  string `json:"state"` // idle | run | practice
		Tries  int    `json:"tries"`
	}
	type rrow struct {
		ID       string  `json:"id"`
		PID      string  `json:"pid"`
		Name     string  `json:"name"`
		X        float64 `json:"x"`
		Y        float64 `json:"y"`
		HP       int     `json:"hp"`
		SC       int     `json:"sc"`
		Practice bool    `json:"practice"`
		Stale    bool    `json:"stale"`
	}
	players := make([]prow, 0, len(h.players))
	online := 0
	for _, p := range h.players {
		st := "idle"
		if p.run != nil {
			st = map[bool]string{true: "practice", false: "run"}[p.run.practice]
		}
		t := 0
		if h.cfile != nil {
			t = h.cfile.Tries[p.key()]
		}
		players = append(players, prow{p.id, p.sid, p.name, p.conn != nil, st, t})
		if p.conn != nil {
			online++
		}
	}
	sort.Slice(players, func(i, j int) bool {
		if players[i].SID != players[j].SID {
			return players[i].SID < players[j].SID
		}
		return players[i].Name < players[j].Name
	})
	runs := make([]rrow, 0, len(h.runs))
	for _, r := range h.runs {
		runs = append(runs, rrow{r.id, r.p.id, r.p.name, r1(r.x), r1(r.y), r.hp, r.sc, r.practice, now.Sub(r.at) > 3*time.Second})
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].X > runs[j].X })
	m := map[string]any{"t": "host", "players": players, "runs": runs, "online": online,
		"board": h.leaderboard()}
	if h.course != nil {
		m["course"] = h.courseMeta()
		m["totalRuns"] = len(h.cfile.Runs)
	}
	return mustJSON(m)
}

// ── WebSocket ───────────────────────────────────────────────

func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c := &client{hub: h, send: make(chan []byte, 64)}
	if q.Get("role") == "host" {
		if !isLoopback(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		c.host = true
	} else {
		c.name = cleanText(q.Get("name"), 12)
		c.sid = cleanText(q.Get("sid"), 8)
		c.token = cleanText(q.Get("token"), 40)
		if c.name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if c.token == "" {
			c.token = newID()
		}
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS 업그레이드 실패: %v", err)
		return
	}
	c.conn = conn
	h.register <- c
	go c.writePump()
	go c.readPump()
}

func (c *client) readPump() {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(512 * 1024) // 긴 코스의 버튼 기록(end)도 받을 수 있게
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		c.hub.inbound <- inMsg{c: c, data: msg}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ── 유틸 ────────────────────────────────────────────────────

func label(sid, name string) string {
	if sid != "" {
		return sid + " " + name
	}
	return name
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("JSON 인코딩 실패: %v", err)
		return []byte(`{}`)
	}
	return b
}

// cleanText 는 앞뒤 공백·제어문자를 없애고 글자 수를 제한한다.
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	const hexd = "0123456789abcdef"
	out := make([]byte, 12)
	for i, v := range b {
		out[i*2] = hexd[v>>4]
		out[i*2+1] = hexd[v&0x0f]
	}
	return string(out)
}

func randSeed() uint64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:]) >> 12 // JS 에 보낼 일은 없지만 파일에서 읽기 좋게 53비트 이하
}
