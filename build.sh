#!/usr/bin/env bash
# 교사 배포용 Windows exe 빌드. 리눅스·맥에서도 그대로 크로스컴파일된다(CGO 불필요 — 순수 Go).
#
#   ./build.sh                                  # dist/teavel-submarine.exe (콘솔 창 보임 = 닫으면 종료)
#   ./build.sh dist/teavel-submarine.exe gui    # 콘솔 창 없이(-H windowsgui)
set -euo pipefail

OUT="${1:-dist/teavel-submarine.exe}"
MODE="${2:-console}"
mkdir -p "$(dirname "$OUT")"

LDFLAGS="-s -w"
if [ "$MODE" = "gui" ]; then
  LDFLAGS="-H windowsgui -s -w"
fi

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "$LDFLAGS" -o "$OUT" .

echo "built: $OUT ($(du -h "$OUT" | cut -f1))  mode=$MODE"
