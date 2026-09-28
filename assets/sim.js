// 잠수함 물리 — sim.go 와 한 줄씩 같은 계산(서버가 같은 입력으로 다시 계산해 점수를 확정한다).
// 바꾸면 sim.go 도 같이 바꾸고 `go test` (sim_test.go 가 node 로 이 파일을 돌려 비교)로 확인할 것.
(function (root) {
  'use strict';
  const C = {
    DT: 1 / 60, SPEED: 10, DEPTH: 60, YMIN: 1.2, YMAX: 58.4,
    AIR_IN: 0.9, AIR_OUT: 0.35, PDEPTH: 30, G: 9, DRAG: 0.9,
    KNOCK: 4, BOUNCE: 3, PEARL_R: 1.2, LOOKX: 6, MAXOBSW: 40,
    HP: 3, INV: 90, START_Y: 10, START_AIR: 0.5,
    PEARL: 30, FINISH: 300, HULLBON: 100,
  };
  const HULL_DX = [-3.4, 0, 3.4];
  const HULL_R = [1.25, 1.6, 1.25];

  function Sim(course) {
    this.obs = course.obs;       // x1 오름차순
    this.pearls = course.pearls; // x 오름차순
    this.length = course.length;
    this.tick = 0; this.x = 0; this.y = C.START_Y; this.vy = 0; this.air = C.START_AIR;
    this.hp = C.HP; this.inv = 0; this.hits = 0; this.nPearls = 0;
    this.got = new Array(course.pearls.length).fill(false);
    this.done = ''; this.oi = 0; this.pi = 0;
    this.lastHit = null; // 화면 효과용(물리엔 영향 없음)
  }

  Sim.prototype.step = function (pressed) {
    if (this.done) return;
    this.tick++;
    if (this.inv > 0) this.inv--;
    this.lastHit = null;

    if (pressed) {
      this.air = this.air + C.AIR_IN * C.DT;
      if (this.air > 1) this.air = 1;
    } else {
      const rate = C.AIR_OUT * (1 + this.y / C.PDEPTH);
      this.air = this.air - rate * C.DT;
      if (this.air < 0) this.air = 0;
    }

    const acc = C.G * (1 - 2 * this.air) - C.DRAG * this.vy;
    this.vy = this.vy + acc * C.DT;
    this.y = this.y + this.vy * C.DT;
    this.x = this.tick * C.SPEED / 60;

    if (this.y < C.YMIN) {
      this.y = C.YMIN;
      if (this.vy < 0) this.vy = 0;
    }
    if (this.y > C.YMAX) {
      this.y = C.YMAX;
      if (this.vy > 0) this.vy = -C.BOUNCE;
      this.damage('bed');
    }

    const obs = this.obs;
    while (this.oi < obs.length && obs[this.oi].x1 < this.x - C.LOOKX - C.MAXOBSW) this.oi++;
    for (let j = this.oi; j < obs.length && obs[j].x1 <= this.x + C.LOOKX; j++) {
      const o = obs[j];
      if (o.x2 < this.x - C.LOOKX) continue;
      if (this.hitsObs(o)) {
        if (this.inv === 0) {
          if (o.k === 'rock') this.vy = -C.KNOCK;
          else if (o.k === 'ice') this.vy = C.KNOCK;
          else if (this.y < o.cy) this.vy = -C.KNOCK;
          else this.vy = C.KNOCK;
        }
        this.damage(o.k);
        break;
      }
    }

    const ps = this.pearls;
    while (this.pi < ps.length && ps[this.pi].x < this.x - C.LOOKX) this.pi++;
    for (let j = this.pi; j < ps.length && ps[j].x <= this.x + C.LOOKX; j++) {
      if (this.got[j]) continue;
      const p = ps[j];
      for (let k = 0; k < 3; k++) {
        const dx = this.x + HULL_DX[k] - p.x;
        const dy = this.y - p.y;
        const rr = HULL_R[k] + C.PEARL_R;
        if (dx * dx + dy * dy < rr * rr) {
          this.got[j] = true;
          this.nPearls++;
          this.lastPearl = j;
          break;
        }
      }
    }

    if (this.hp <= 0) this.done = 'crash';
    else if (this.x >= this.length) this.done = 'finish';
  };

  Sim.prototype.damage = function (kind) {
    if (this.inv > 0) return;
    this.hp--;
    this.hits++;
    this.inv = C.INV;
    this.lastHit = kind;
  };

  Sim.prototype.hitsObs = function (o) {
    for (let k = 0; k < 3; k++) {
      const cx = this.x + HULL_DX[k];
      const cy = this.y;
      const r = HULL_R[k];
      if (o.k === 'mine') {
        const dx = cx - o.cx;
        const dy = cy - o.cy;
        const rr = r + o.r;
        if (dx * dx + dy * dy < rr * rr) return true;
        continue;
      }
      const nx = Math.max(o.x1, Math.min(cx, o.x2));
      const ny = Math.max(o.y1, Math.min(cy, o.y2));
      const dx = cx - nx;
      const dy = cy - ny;
      if (dx * dx + dy * dy < r * r) return true;
    }
    return false;
  };

  Sim.prototype.dist = function () { return Math.floor(Math.min(this.x, this.length)); };

  Sim.prototype.score = function () {
    let sc = this.dist() + C.PEARL * this.nPearls;
    if (this.done === 'finish') sc += C.FINISH + C.HULLBON * this.hp;
    return sc;
  };

  function maxTicks(length) { return Math.floor(length * 60 / C.SPEED) + 10; }

  function replay(course, toggles) {
    const s = new Sim(course);
    let pressed = false, j = 0;
    const limit = maxTicks(course.length);
    while (!s.done && s.tick < limit) {
      while (j < toggles.length && toggles[j] === s.tick) { pressed = !pressed; j++; }
      s.step(pressed);
    }
    return s;
  }

  const api = { C, Sim, replay, maxTicks, HULL_DX, HULL_R };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else root.SubSim = api;
})(typeof window !== 'undefined' ? window : this);
