#!/bin/sh
set -eu

grep -F 'docs/backup-restore.md' README.md >/dev/null
grep -F 'agent-studio backup create' README.md >/dev/null
grep -F 'agent-studio backup inspect' README.md >/dev/null
grep -F 'agent-studio backup restore' README.md >/dev/null

for text in \
  '文件权限为 0600，但备份包未加密' \
  '备份包含完整运行输入、输出和节点错误详情' \
  '使用加密磁盘或外部加密工具' \
  '停止所有旧版本 API' \
  '仅允许恢复到空实例' \
  '不会续跑活动运行' \
  'BACKUP_CHECKSUM_MISMATCH' \
  'BACKUP_API_RUNNING' \
  'BACKUP_TARGET_NOT_EMPTY'; do
  grep -F "$text" docs/backup-restore.md >/dev/null
done

grep -F 'backup restore --dry-run' docs/backup-restore.md >/dev/null
grep -F 'backup restore --confirm-empty-instance' docs/backup-restore.md >/dev/null

if ! awk '
  /^   DATABASE_URL=/ { assigned = 1; next }
  assigned && /^   export DATABASE_URL$/ { exported = 1; next }
  exported && /^   psql "\$DATABASE_URL" -v ON_ERROR_STOP=1 -c / { found = 1; exit }
  { assigned = 0; exported = 0 }
  END { exit !found }
' docs/backup-restore.md; then
  printf '%s\n' 'backup docs must assign and export DATABASE_URL before invoking psql' >&2
  exit 1
fi

for forbidden in \
  '备份包已内置加密' \
  '可以合并恢复到非空实例' \
  '系统会自动定时备份' \
  '活动运行会在重启后续跑'; do
  if grep -F "$forbidden" README.md docs/backup-restore.md >/dev/null; then
    printf 'forbidden backup claim: %s\n' "$forbidden" >&2
    exit 1
  fi
done

if grep -F -- '--confirm-empty-instance' README.md >/dev/null; then
  printf '%s\n' 'README must link to the formal restore checklist instead of embedding its destructive command' >&2
  exit 1
fi
