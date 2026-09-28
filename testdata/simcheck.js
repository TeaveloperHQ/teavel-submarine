// sim_test.go 가 부르는 검증기: stdin 으로 {course, cases:[toggles...]} 를 받아 각 주행 결과를 JSON 으로 낸다.
const { replay } = require('../assets/sim.js');
let buf = '';
process.stdin.on('data', d => { buf += d; });
process.stdin.on('end', () => {
  const { course, cases } = JSON.parse(buf);
  const out = cases.map(tg => {
    const s = replay(course, tg);
    return { tick: s.tick, x: s.x, y: s.y, vy: s.vy, air: s.air, hp: s.hp, pearls: s.nPearls, score: s.score(), done: s.done };
  });
  process.stdout.write(JSON.stringify(out));
});
