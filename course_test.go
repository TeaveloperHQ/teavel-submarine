package main

import (
	"math"
	"reflect"
	"testing"
)

// autopilot 는 0.1초마다 "누름/뗌"을 고르는 빔 탐색으로 한 번도 안 부딪히고 완주하는 입력이 있는지 찾는다.
// 찾으면 그 입력(토글 틱 목록)을 돌려준다. 코스 생성기가 "통과 불가능한 코스"를 만들지 않는지 보는 용도.
func autopilot(c *Course) ([]int, bool) {
	type node struct {
		s       *Sim
		pressed bool
		toggles []int
	}
	const every, beam = 6, 400
	nodes := []node{{s: newSim(c.Obs, c.Pearls, c.Length)}}
	for len(nodes) > 0 {
		next := []node{}
		seen := map[[3]int]bool{}
		for _, n := range nodes {
			for _, pr := range []bool{false, true} {
				s := n.s.clone()
				tg := n.toggles
				if pr != n.pressed {
					tg = append(append([]int(nil), tg...), s.Tick)
				}
				for i := 0; i < every && s.Done == ""; i++ {
					s.Step(pr)
				}
				if s.Hits > 0 {
					continue
				}
				if s.Done == "finish" {
					return tg, true
				}
				k := [3]int{int(math.Round(s.Y * 3)), int(math.Round(s.VY * 3)), int(math.Round(s.Air * 12))}
				if seen[k] {
					continue
				}
				seen[k] = true
				next = append(next, node{s: s, pressed: pr, toggles: tg})
			}
		}
		if len(next) > beam { // 고르게 솎아 낸다(수심 다양성 유지)
			thin := make([]node, 0, beam)
			for i := 0; i < beam; i++ {
				thin = append(thin, next[i*len(next)/beam])
			}
			next = thin
		}
		nodes = next
	}
	return nil, false
}

func TestCoursesArePassable(t *testing.T) {
	for _, d := range []string{"easy", "normal", "hard"} {
		for seed := uint64(1); seed <= 6; seed++ {
			c := &Course{Seed: seed * 7919, Length: 900, Diff: d}
			c.build()
			tg, ok := autopilot(c)
			if !ok {
				t.Errorf("%s seed %d: 무사고 완주 경로가 없음 (장애물 %d개)", d, c.Seed, len(c.Obs))
				continue
			}
			s := replay(c, tg)
			if s.Done != "finish" || s.Hits != 0 {
				t.Errorf("%s seed %d: 재현 실패 %s hits=%d", d, c.Seed, s.Done, s.Hits)
			}
			t.Logf("%s seed %d: 장애물 %d 진주 %d · 자동 조종 점수 %d (진주 %d, 버튼 %d번)",
				d, c.Seed, len(c.Obs), len(c.Pearls), s.Score(), s.Pearls, len(tg)/2)
		}
	}
}

func TestCourseDeterministic(t *testing.T) {
	a1, p1 := generateCourse(42, 1200, "hard")
	a2, p2 := generateCourse(42, 1200, "hard")
	if !reflect.DeepEqual(a1, a2) || !reflect.DeepEqual(p1, p2) {
		t.Fatal("같은 시드인데 코스가 다름")
	}
	for i := 1; i < len(a1); i++ {
		if a1[i].X1 < a1[i-1].X1 {
			t.Fatal("장애물이 x1 순서가 아님")
		}
		if a1[i].X2-a1[i].X1 > simMaxObsW {
			t.Fatal("장애물 폭이 simMaxObsW 초과")
		}
	}
}

func TestPhysicsFeel(t *testing.T) {
	// 가만히 두면(버튼 안 누름) 가라앉아 해저에 닿고, 계속 누르면 수면까지 떠오른다.
	s := newSim(nil, nil, 2000)
	for i := 0; i < 600; i++ {
		s.Step(false)
	}
	if s.Hits == 0 {
		t.Errorf("10초 동안 안 눌렀는데 해저에 안 닿음 y=%.1f", s.Y)
	}
	s = newSim(nil, nil, 2000)
	s.Y = 50
	for i := 0; i < 600; i++ {
		s.Step(true)
	}
	if s.Y != simYMin {
		t.Errorf("10초 동안 눌렀는데 수면에 못 올라옴 y=%.1f", s.Y)
	}
}
