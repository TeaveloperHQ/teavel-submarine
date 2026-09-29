"""부력 잠수함 아이콘 생성기 — python gen.py 로 icon.svg / icon-small.svg 를 다시 만든다.
래스터·ico·exe 리소스는 README 의 "아이콘" 절 명령으로(node render.js)."""

# teaveloper 공용 규격: 800x800 라운드 사각형(rx 184) · 공식 그라데이션 · 죽방.
# 이 앱: 간단한 흰 잠수함 한 척, 선체에 죽방 엠블럼 하나(배경 그라데이션 색으로 파냄).
# 그림이 단순해 작은 크기(16·32·48px)도 같은 그림을 쓴다.


def svg(note):
    cx, cy = 512, 590                    # 선체 중심
    x0, x1, h = 236, 866, 300            # 선체 뒤끝·앞끝·높이
    r = h / 2
    # 죽방: vector-soccer 와 같은 비율(원 r72 · 꼭짓점 +48 · 밑변 172 · 밑변 높이 +204)을 r48 로 줄임
    ex, ey, er = cx + 40, cy - 44, 48
    return f'''<svg width="1024" height="1024" viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg">
  <!--
    부력 잠수함(teavel-submarine) 아이콘 — {note}. gen.py 로 생성.
    teaveloper 공용 규격: 800x800 라운드 사각형(rx 184) · 공식 그라데이션 · 죽방.
    흰 잠수함 한 척, 선체 가운데 죽방 엠블럼(배경색으로 파냄).
  -->
  <defs>
    <linearGradient id="g" gradientUnits="userSpaceOnUse" x1="112" y1="112" x2="912" y2="912">
      <stop offset="0%"   stop-color="#6366f1"/>
      <stop offset="50%"  stop-color="#8b5cf6"/>
      <stop offset="100%" stop-color="#06b6d4"/>
    </linearGradient>
  </defs>

  <rect x="112" y="112" width="800" height="800" rx="184" fill="url(#g)"/>

  <!-- 잠수함: 잠망경 · 탑 · 선체(앞 둥글고 뒤 좁음) · 꼬리 날개 -->
  <path d="M{ex + 28} {cy - r - 100} V{cy - r - 190} H{ex + 96}" fill="none" stroke="#ffffff" stroke-width="26" stroke-linejoin="round"/>
  <rect x="{ex + 84}" y="{cy - r - 212}" width="34" height="44" rx="8" fill="#ffffff"/>
  <g fill="#ffffff">
    <path d="M{ex - 100} {cy - r + 4} L{ex - 74} {cy - r - 110} H{ex + 74} L{ex + 100} {cy - r + 4} Z"/>
    <path d="M{x0} {cy - 70} Q{x0 + 110} {cy - r} {x0 + 230} {cy - r} H{x1 - r} A{r} {r} 0 0 1 {x1 - r} {cy + r} H{x0 + 230} Q{x0 + 110} {cy + r} {x0} {cy + 70} Z"/>
    <path d="M{x0 + 30} {cy - 30} L{x0 - 40} {cy - 130} H{x0 - 78} L{x0 - 20} {cy} L{x0 - 78} {cy + 130} H{x0 - 40} L{x0 + 30} {cy + 30} Z"/>
  </g>

  <!-- 죽방 엠블럼 -->
  <g fill="url(#g)">
    <circle cx="{ex}" cy="{ey}" r="{er}"/>
    <path d="M{ex} {ey + 32} L{ex - 57} {ey + 136} L{ex + 57} {ey + 136} Z"/>
  </g>
</svg>
'''


if __name__ == "__main__":
    open("icon.svg", "w", encoding="utf-8").write(svg("큰 크기용(256px 이상)"))
    open("icon-small.svg", "w", encoding="utf-8").write(svg("작은 크기용(16·32·48px)"))
