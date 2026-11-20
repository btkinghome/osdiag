#!/bin/sh
# 端到端：交叉编译 → 上传 → 目标机运行 → 取回 → inspect → schema 校验。
#
# 用法：
#   scripts/e2e.sh [quick|standard|deep]
# 环境变量：
#   OSDIAG_HOST      目标主机（默认 10.50.28.20）
#   OSDIAG_KEY       SSH 私钥（默认 ~/.ssh/id_ed25519）
#   OSDIAG_USER      SSH 用户（默认 root）
#   OSDIAG_DURATION  采样秒数（默认 20）
#   OSDIAG_EXTRA     额外参数，如 "--allow-heavy --allow-external"
#   OSDIAG_JOURNAL   1 表示先导出 journal 再导入
set -eu

ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
HOST=${OSDIAG_HOST:-10.50.28.20}
USER=${OSDIAG_USER:-root}
KEY=${OSDIAG_KEY:-$HOME/.ssh/id_ed25519}
PROFILE=${1:-quick}
DURATION=${OSDIAG_DURATION:-20}
EXTRA=${OSDIAG_EXTRA:-}
REMOTE=/tmp/osdiag
WORK=/tmp/osdiag-work
SSH="ssh -o BatchMode=yes -o IdentitiesOnly=yes -i $KEY -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -o LogLevel=ERROR $USER@$HOST"

export GOCACHE="$ROOT/.gocache" GOTMPDIR="$ROOT/.tmp"
mkdir -p "$GOCACHE" "$GOTMPDIR" "$ROOT/lab"

echo "== 构建 linux/amd64"
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X luanbird/osdiag/internal/version.Version=0.1.0-m1" \
  -o lab/osdiag_linux_amd64 ./cmd/osdiag )

echo "== 上传"
scp -q -o BatchMode=yes -o IdentitiesOnly=yes -i "$KEY" \
  -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no \
  "$ROOT/lab/osdiag_linux_amd64" "$USER@$HOST:$REMOTE"
$SSH "chmod 0755 $REMOTE && $REMOTE version"

JOURNAL_ARGS=""
if [ "${OSDIAG_JOURNAL:-0}" = "1" ]; then
  echo "== 导出 journal 文本"
  $SSH "journalctl -o export -n 5000 > /tmp/osdiag-journal.export"
  JOURNAL_ARGS="--journal-file /tmp/osdiag-journal.export"
fi

echo "== 运行（$PROFILE ${DURATION}s）"
OUT=$($SSH "$REMOTE run --profile $PROFILE --duration $DURATION --workdir $WORK $JOURNAL_ARGS $EXTRA" 2>&1 || true)
echo "$OUT"
ARCHIVE=$(printf '%s\n' "$OUT" | sed -n 's/^ARCHIVE=//p' | tail -1)
[ -n "$ARCHIVE" ] || { echo "未拿到 ARCHIVE 路径" >&2; exit 1; }

echo "== 取回 $ARCHIVE"
BASE=$(basename "$ARCHIVE")
scp -q -o BatchMode=yes -o IdentitiesOnly=yes -i "$KEY" \
  -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no \
  "$USER@$HOST:$ARCHIVE" "$ROOT/lab/$BASE"

echo "== inspect"
( cd "$ROOT" && ./bin/osdiag inspect "lab/$BASE" 2>/dev/null || \
  go run ./cmd/osdiag inspect "lab/$BASE" )

echo "== 解包并 schema 校验"
DEST="$ROOT/lab/${BASE%.tar.gz}"
rm -rf "$DEST" && mkdir -p "$DEST"
tar -xzf "$ROOT/lab/$BASE" -C "$DEST"
python3 "$ROOT/../docs/osdiag/tools/validate-package.py" "$DEST"

echo "== 通过：$ROOT/lab/$BASE"
