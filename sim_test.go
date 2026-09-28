package main

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"testing"
)

// TestSimMatchesJS 는 브라우저 물리(assets/sim.js)와 서버 물리(sim.go)가 비트 단위로 같은지 node 로 확인한다.
// node 가 없으면 건너뛴다.
func TestSimMatchesJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node 없음 — JS/Go 물리 비교 생략")
	}
	type result struct {
		Tick   int     `json:"tick"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		VY     float64 `json:"vy"`
		Air    float64 `json:"air"`
		HP     int     `json:"hp"`
		Pearls int     `json:"pearls"`
		Score  int     `json:"score"`
		Done   string  `json:"done"`
	}
	for _, d := range []string{"easy", "normal", "hard"} {
		c := &Course{Seed: 12345, Length: 600, Diff: d}
		c.build()
		cases := [][]int{{}}
		if tg, ok := autopilot(c); ok {
			cases = append(cases, tg)
		}
		r := rand.New(rand.NewPCG(1, 2))
		for i := 0; i < 20; i++ { // 아무렇게나 누르는 학생
			tg := []int{}
			for tk := r.IntN(40); tk < maxTicks(c.Length); tk += 1 + r.IntN(90) {
				tg = append(tg, tk)
			}
			cases = append(cases, tg)
		}
		in, _ := json.Marshal(map[string]any{
			"course": map[string]any{"obs": c.Obs, "pearls": c.Pearls, "length": c.Length},
			"cases":  cases,
		})
		cmd := exec.Command(node, "testdata/simcheck.js")
		cmd.Stdin = bytes.NewReader(in)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("node 실행 실패: %v", err)
		}
		var js []result
		if err := json.Unmarshal(out, &js); err != nil {
			t.Fatalf("node 출력 해석 실패: %v", err)
		}
		for i, tg := range cases {
			s := replay(c, tg)
			g := result{s.Tick, s.X, s.Y, s.VY, s.Air, s.HP, s.Pearls, s.Score(), s.Done}
			if g != js[i] {
				t.Errorf("%s 경우 %d: Go %+v\n            JS %+v", d, i, g, js[i])
			}
		}
	}
}
