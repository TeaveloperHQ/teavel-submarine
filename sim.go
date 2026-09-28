package main

import "math"

// 잠수함 물리. 학생 브라우저(assets/sim.js)와 서버가 **똑같은 계산**을 한다.
//
//   - 학생 화면은 60Hz 고정 틱으로 이 물리를 돌리며 즉시 반응한다(버튼 지연 없음).
//   - 주행이 끝나면 학생은 "버튼을 누르고 뗀 틱 번호" 목록만 보낸다.
//   - 서버는 같은 코스·같은 입력으로 다시 계산해 **서버가 낸 점수만** 기록한다(점수 조작 불가).
//
// 두 구현이 비트 단위로 같으려면:
//   - 사칙연산·비교·floor 만 쓴다(sin·exp 같은 초월함수는 JS/Go 결과가 다를 수 있다).
//   - 물리 값은 const 가 아니라 var 로 둔다. Go 상수식은 무한 정밀도로 접혀 JS 와 반올림이 달라진다.
//   - 곱을 더하는 식은 float64(...) 로 감싸 FMA(곱셈-덧셈 융합)를 막는다(Go 명세: 명시적 변환은 융합 금지).
//   - 계산 순서를 sim.js 와 한 줄씩 맞춘다. 바꾸면 반드시 양쪽을 같이 바꾸고 go test 로 확인.
//
// 좌표: x = 전진 거리(m), y = 수심(m, 아래가 +). 바다 깊이 60m.

var (
	simDT      = 1.0 / 60
	simSpeed   = 10.0 // 전진 속도 m/s(일정)
	simDepth   = 60.0 // 해저 수심
	simYMin    = 1.2  // 수면에 떠 있을 때 선체 중심 수심
	simYMax    = 58.4 // 해저에 닿는 선체 중심 수심(60 - 선체 반높이 1.6)
	simAirIn   = 0.9  // 공기 주입 속도(탱크 비율/초) — 고압 공기가 물을 밀어낸다
	simAirOut  = 0.35 // 수면에서 공기가 빠지는 속도(탱크 비율/초)
	simPDepth  = 30.0 // 수압 계수: 깊이 30m 마다 물이 들어오는 속도가 수면의 1배씩 더 빨라진다
	simG       = 9.0  // 탱크가 텅 빌 때/가득 찰 때 알짜힘에 의한 가속도 m/s²
	simDrag    = 0.9  // 물의 저항(속도에 비례) — 종단 속도 = G/Drag = 10 m/s
	simKnock   = 4.0  // 장애물에 부딪혔을 때 튕겨 나가는 속도
	simBounce  = 3.0  // 해저에 닿았을 때 튕겨 오르는 속도
	simPearlR  = 1.2  // 진주 크기(반지름)
	simLookX   = 6.0  // 충돌 검사 범위(선체 앞뒤 끝 4.65m 보다 넉넉히)
	simMaxObsW = 40.0 // 장애물 최대 폭(x1 정렬 목록에서 건너뛰기 기준)
)

const (
	simHP       = 3  // 선체 내구도
	simInvTicks = 90 // 부딪힌 뒤 무적 시간(1.5초)
	simStartY   = 10.0
	simStartAir = 0.5 // 중성 부력(뜨지도 가라앉지도 않음)

	scorePearl   = 30
	scoreFinish  = 300
	scoreHullBon = 100 // 완주 시 남은 선체 1칸당
)

// 선체 충돌 원 3개(앞·가운데·뒤). 함교(탑)는 봐준다.
var hullDX = [3]float64{-3.4, 0, 3.4}
var hullR = [3]float64{1.25, 1.6, 1.25}

// Obstacle 은 장애물 하나. rock=해저에서 솟은 바위, ice=수면에서 내려온 빙산(둘 다 사각형 판정),
// mine=기뢰(원 판정, 중심 cx·cy 반지름 r — x1..x2·y1..y2 는 경계 상자).
type Obstacle struct {
	K  string  `json:"k"`
	X1 float64 `json:"x1"`
	X2 float64 `json:"x2"`
	Y1 float64 `json:"y1"`
	Y2 float64 `json:"y2"`
	CX float64 `json:"cx"`
	CY float64 `json:"cy"`
	R  float64 `json:"r"`
}

type Pearl struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Sim struct {
	obs    []Obstacle // X1 오름차순
	pearls []Pearl    // X 오름차순
	length float64

	Tick   int
	X, Y   float64
	VY     float64 // 아래가 +
	Air    float64 // 밸러스트 탱크 속 공기 비율 0(물 가득)..1(공기 가득)
	HP     int
	Inv    int
	Hits   int
	Pearls int
	Got    []bool
	Done   string // "" | "finish" | "crash"
	oi, pi int
}

func newSim(obs []Obstacle, pearls []Pearl, length int) *Sim {
	return &Sim{obs: obs, pearls: pearls, length: float64(length),
		Y: simStartY, Air: simStartAir, HP: simHP, Got: make([]bool, len(pearls))}
}

func (s *Sim) clone() *Sim {
	c := *s
	c.Got = append([]bool(nil), s.Got...)
	return &c
}

// Step 은 1/60초 진행한다. pressed = 버튼을 누르고 있음(공기 주입).
func (s *Sim) Step(pressed bool) {
	if s.Done != "" {
		return
	}
	s.Tick++
	if s.Inv > 0 {
		s.Inv--
	}

	// ① 밸러스트 탱크: 누르면 고압 공기가 물을 밀어내고, 떼면 벤트가 열려 수압으로 물이 들어온다.
	if pressed {
		s.Air = s.Air + float64(simAirIn*simDT)
		if s.Air > 1 {
			s.Air = 1
		}
	} else {
		rate := simAirOut * (1 + s.Y/simPDepth)
		s.Air = s.Air - float64(rate*simDT)
		if s.Air < 0 {
			s.Air = 0
		}
	}

	// ② 알짜힘 = 무게 − 부력. 공기 0.5 일 때 중성 부력.
	acc := float64(simG*(1-float64(2*s.Air))) - float64(simDrag*s.VY)
	s.VY = s.VY + float64(acc*simDT)
	s.Y = s.Y + float64(s.VY*simDT)
	s.X = float64(s.Tick) * simSpeed / 60

	// ③ 수면·해저
	if s.Y < simYMin {
		s.Y = simYMin
		if s.VY < 0 {
			s.VY = 0
		}
	}
	if s.Y > simYMax {
		s.Y = simYMax
		if s.VY > 0 {
			s.VY = -simBounce
		}
		s.damage()
	}

	// ④ 장애물
	for s.oi < len(s.obs) && s.obs[s.oi].X1 < s.X-simLookX-simMaxObsW {
		s.oi++
	}
	for j := s.oi; j < len(s.obs) && s.obs[j].X1 <= s.X+simLookX; j++ {
		o := &s.obs[j]
		if o.X2 < s.X-simLookX {
			continue
		}
		if s.hits(o) {
			if s.Inv == 0 {
				switch {
				case o.K == "rock":
					s.VY = -simKnock
				case o.K == "ice":
					s.VY = simKnock
				case s.Y < o.CY:
					s.VY = -simKnock
				default:
					s.VY = simKnock
				}
			}
			s.damage()
			break
		}
	}

	// ⑤ 진주
	for s.pi < len(s.pearls) && s.pearls[s.pi].X < s.X-simLookX {
		s.pi++
	}
	for j := s.pi; j < len(s.pearls) && s.pearls[j].X <= s.X+simLookX; j++ {
		if s.Got[j] {
			continue
		}
		p := s.pearls[j]
		for k := 0; k < 3; k++ {
			dx := s.X + hullDX[k] - p.X
			dy := s.Y - p.Y
			rr := hullR[k] + simPearlR
			if float64(dx*dx)+float64(dy*dy) < float64(rr*rr) {
				s.Got[j] = true
				s.Pearls++
				break
			}
		}
	}

	if s.HP <= 0 {
		s.Done = "crash"
	} else if s.X >= s.length {
		s.Done = "finish"
	}
}

func (s *Sim) damage() {
	if s.Inv > 0 {
		return
	}
	s.HP--
	s.Hits++
	s.Inv = simInvTicks
}

func (s *Sim) hits(o *Obstacle) bool {
	for k := 0; k < 3; k++ {
		cx := s.X + hullDX[k]
		cy := s.Y
		r := hullR[k]
		if o.K == "mine" {
			dx := cx - o.CX
			dy := cy - o.CY
			rr := r + o.R
			if float64(dx*dx)+float64(dy*dy) < float64(rr*rr) {
				return true
			}
			continue
		}
		nx := math.Max(o.X1, math.Min(cx, o.X2))
		ny := math.Max(o.Y1, math.Min(cy, o.Y2))
		dx := cx - nx
		dy := cy - ny
		if float64(dx*dx)+float64(dy*dy) < float64(r*r) {
			return true
		}
	}
	return false
}

func (s *Sim) Dist() int {
	return int(math.Floor(math.Min(s.X, s.length)))
}

func (s *Sim) Score() int {
	sc := s.Dist() + scorePearl*s.Pearls
	if s.Done == "finish" {
		sc += scoreFinish + scoreHullBon*s.HP
	}
	return sc
}

// maxTicks 는 완주에 필요한 틱 수(+여유).
func maxTicks(length int) int { return length*60/int(simSpeed) + 10 }

// replay 는 학생이 보낸 입력(버튼 상태가 바뀐 틱 번호, 오름차순, 처음엔 떼어 둔 상태)으로 주행을 다시 계산한다.
func replay(c *Course, toggles []int) *Sim {
	s := newSim(c.Obs, c.Pearls, c.Length)
	pressed := false
	j := 0
	limit := maxTicks(c.Length)
	for s.Done == "" && s.Tick < limit {
		for j < len(toggles) && toggles[j] == s.Tick {
			pressed = !pressed
			j++
		}
		s.Step(pressed)
	}
	return s
}
