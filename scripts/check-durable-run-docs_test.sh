#!/bin/sh
set -eu

require_text() {
  document=$1
  pattern=$2
  [ -f "$document" ] || {
    printf '%s\n' "missing required document: $document" >&2
    exit 1
  }
  grep -Eq "$pattern" "$document" || {
    printf '%s\n' "missing required text in $document: $pattern" >&2
    exit 1
  }
}

require_text README.md 'RUN_PAYLOAD_ENCRYPTION_KEY'
require_text README.md 'make dev-stack'
require_text README.md 'API.*Worker|Worker.*API'
require_text README.md '人工恢复|恢复入口'

require_text docs/run-management.md '人工恢复'
require_text docs/run-management.md '重试.*副作用|副作用.*重试'
require_text docs/run-management.md '终止'

require_text docs/backup-restore.md 'v1alpha1'
require_text docs/backup-restore.md 'v1alpha2'
require_text docs/backup-restore.md '相同.*RUN_PAYLOAD_ENCRYPTION_KEY|RUN_PAYLOAD_ENCRYPTION_KEY.*相同'
require_text docs/backup-restore.md '错误密钥|密钥错误'

require_text docs/observability.md 'queue|队列深度'
require_text docs/observability.md 'lease|租约'
require_text docs/observability.md 'recovery|人工恢复'
require_text docs/observability.md 'drain|优雅关闭'

require_text docs/upgrades/v0.5-d.md '停止.*旧.*API'
require_text docs/upgrades/v0.5-d.md 'migration 7'
require_text docs/upgrades/v0.5-d.md '同版本.*API.*Worker'
require_text docs/upgrades/v0.5-d.md '禁止.*混跑|不支持.*混合运行'
require_text docs/upgrades/v0.5-d.md 'legacy_active_run|遗留活动运行'
require_text docs/upgrades/v0.5-d.md '回滚'

printf '%s\n' 'durable run documentation contract passed'
