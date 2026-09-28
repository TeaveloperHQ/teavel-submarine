package main

import (
	"math"
	"math/rand/v2"
	"sort"
)

// 코스(맵) 생성. 같은 시드·길이·난이도면 언제나 같은 코스가 나온다 — 코스 파일에는 이 셋만 저장하고
// 서버를 다시 켜면 장애물을 다시 만든다. 학생에게는 완성된 장애물 목록을 그대로 보낸다(JS 는 생성하지 않음).
//
// 만드는 법: 먼저 "지나갈 수 있는 길"(경유점 x·수심 c)을 정하고, 경유점마다 그 길만 비워 둔 장애물을 세운다.
// 경유점 사이 수심 변화는 난이도별 기울기 이하로 제한해 잠수함이 따라갈 수 있게 한다
// (course_test.go 의 자동 조종사가 모든 난이도에서 무사고 완주가 가능한지 확인한다).

type Course struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Seed      uint64 `json:"seed"`
	Length    int    `json:"length"` // m
	Diff      string `json:"diff"`   // easy | normal | hard
	CreatedAt string `json:"createdAt"`
	Limit     int    `json:"limit"` // 공식 도전 횟수 제한(0 = 무제한)
	Open      bool   `json:"open"`  // 기록 중(공식 도전 가능)

	Obs    []Obstacle `json:"-"`
	Pearls []Pearl    `json:"-"`
}

type diffParam struct {
	space0, space1 float64 // 경유점 간격(시작 → 끝으로 갈수록 좁아짐)
	gap0, gap1     float64 // 통과 틈 높이
	slope          float64 // 경유점 사이 최대 수심 변화 / 거리
	mineP, caveP   float64
	extraMine      float64 // 기뢰 하나 더
}

var diffParams = map[string]diffParam{
	"easy":   {space0: 58, space1: 48, gap0: 22, gap1: 18, slope: 0.32, mineP: 0.25, caveP: 0, extraMine: 0},
	"normal": {space0: 48, space1: 38, gap0: 18, gap1: 15, slope: 0.42, mineP: 0.45, caveP: 0.15, extraMine: 0.15},
	"hard":   {space0: 40, space1: 32, gap0: 15.5, gap1: 13.5, slope: 0.5, mineP: 0.6, caveP: 0.3, extraMine: 0.4},
}

var diffKo = map[string]string{"easy": "쉬움", "normal": "보통", "hard": "어려움"}

func validDiff(d string) bool { _, ok := diffParams[d]; return ok }

func (c *Course) build() {
	c.Obs, c.Pearls = generateCourse(c.Seed, c.Length, c.Diff)
}

func rd(v float64) float64 { return math.Round(v*2) / 2 } // 0.5m 단위(전송·파일에서 깔끔)

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func clampF(v, lo, hi float64) float64 { return math.Max(lo, math.Min(v, hi)) }

func generateCourse(seed uint64, length int, diff string) ([]Obstacle, []Pearl) {
	p, ok := diffParams[diff]
	if !ok {
		p = diffParams["normal"]
	}
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	L := float64(length)
	obs := []Obstacle{}
	pearls := []Pearl{}

	rect := func(k string, x, w, y1, y2 float64) {
		obs = append(obs, Obstacle{K: k, X1: rd(x - w/2), X2: rd(x + w/2), Y1: rd(y1), Y2: rd(y2)})
	}
	mine := func(x, y float64) {
		const mr = 1.8
		x, y = rd(x), rd(y)
		obs = append(obs, Obstacle{K: "mine", X1: x - mr, X2: x + mr, Y1: y - mr, Y2: y + mr, CX: x, CY: y, R: mr})
	}
	// gate 는 수심 c 에 높이 g 의 틈만 남기고 위(빙산)·아래(바위)를 막는다. 한쪽이 너무 얕으면 생략.
	gate := func(x, w, c, g float64, both bool) {
		top, bot := c-g/2, c+g/2
		wantIce := top > 4
		wantRock := bot < 56
		if !both && wantIce && wantRock { // 한쪽만: 길에서 먼 쪽을 비운다
			if c < 30 {
				wantIce = false
			} else {
				wantRock = false
			}
		}
		if wantIce {
			rect("ice", x, w, 0, top)
		}
		if wantRock {
			rect("rock", x, w, bot, simDepth)
		}
	}

	prevX, prevC := 0.0, simStartY
	x := 110.0
	for x < L-50 {
		t := x / L
		space := lerp(p.space0, p.space1, t)
		g := lerp(p.gap0, p.gap1, t)
		if x < 200 {
			g += 3 // 첫 몇 개는 넉넉히
		}

		maxD := p.slope * (x - prevX)
		var c float64
		if r.Float64() < 0.6 {
			c = 8 + r.Float64()*44
		} else {
			c = prevC + (r.Float64()*2-1)*12
		}
		c = clampF(c, prevC-maxD, prevC+maxD)
		c = clampF(c, 8, 52)
		c = rd(c)

		// 경유점 사이(가운데): 기뢰·낮은 바위·진주
		xm, cm := (prevX+x)/2, (prevC+c)/2
		if prevX > 0 {
			nm := 0
			if r.Float64() < p.mineP {
				nm = 1
				if r.Float64() < p.extraMine {
					nm = 2
				}
			}
			side := 1.0
			if r.Float64() < 0.5 {
				side = -1
			}
			for i := 0; i < nm; i++ {
				off := g/2 + 3 + r.Float64()*9
				y := cm + side*off
				if y < 5 || y > 55 {
					y = cm - side*off
				}
				if y >= 5 && y <= 55 {
					mine(xm+(r.Float64()*2-1)*4, y)
				}
				side = -side
			}
			if nm == 0 && cm+g/2+4 < 52 && r.Float64() < 0.35 {
				h := 3 + r.Float64()*5
				rect("rock", xm, 5+r.Float64()*6, simDepth-h, simDepth)
			}
			if r.Float64() < 0.5 {
				pearls = append(pearls, Pearl{X: rd(xm), Y: rd(cm)})
			}
		}

		if r.Float64() < p.caveP && x+30 < L-50 {
			// 동굴: 같은 수심으로 뚫린 문 세 개
			for i := 0; i < 3; i++ {
				gate(x+float64(i)*13, 10, c, g+2, true)
			}
			pearls = append(pearls, Pearl{X: rd(x + 13), Y: c})
			prevX = x + 26
		} else {
			w := 6 + r.Float64()*8
			gate(x, w, c, g, r.Float64() < 0.5)
			pearls = append(pearls, Pearl{X: rd(x), Y: c})
			prevX = x
		}
		prevC = c
		x = prevX + space
	}

	sort.SliceStable(obs, func(i, j int) bool { return obs[i].X1 < obs[j].X1 })
	sort.SliceStable(pearls, func(i, j int) bool { return pearls[i].X < pearls[j].X })
	return obs, pearls
}
