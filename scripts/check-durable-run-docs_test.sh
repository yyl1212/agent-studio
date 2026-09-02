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

release_notes=docs/releases/v0.5.0-rc.1.md

require_text "$release_notes" 'v0\.5\.0-rc\.1'
require_text "$release_notes" '0\.5\.0-dev'
require_text "$release_notes" 'Go Node SDK.*0\.5\.0'
require_text "$release_notes" 'agent-studio\.dev/v1alpha1'
require_text "$release_notes" '官方 manifest Runtime 范围.*\[v0\.2\.0, v0\.6\.0\)'
require_text "$release_notes" 'RC|候选版本'
require_text "$release_notes" '非生产稳定版'
require_text "$release_notes" '不提供 v1 兼容承诺'
require_text "$release_notes" 'v0\.5-A'
require_text "$release_notes" 'v0\.5-B'
require_text "$release_notes" 'v0\.5-C'
require_text "$release_notes" 'v0\.5-D'
require_text "$release_notes" '同版本.*API.*Worker|API.*Worker.*同版本'
require_text "$release_notes" '同一数据库|相同数据库'
require_text "$release_notes" '相同密钥|同一密钥'
require_text "$release_notes" '维护窗口'
require_text "$release_notes" 'migration 7'
require_text "$release_notes" 'v1alpha2'
require_text "$release_notes" 'v1alpha1'
require_text "$release_notes" '错误密钥'
require_text "$release_notes" '人工恢复'
require_text "$release_notes" '1 API'
require_text "$release_notes" '1 Worker'
require_text "$release_notes" 'concurrency 4|并发 4'
require_text "$release_notes" '500 Mock runs'
require_text "$release_notes" '10 分钟'
require_text "$release_notes" '不是 SLA'
require_text "$release_notes" '隔离.*非生产|非生产.*隔离'
require_text "$release_notes" '专用.*测试密钥|测试密钥.*专用'
require_text "$release_notes" 'RUN_PAYLOAD_ENCRYPTION_KEY.*openssl rand -base64 32'
require_text "$release_notes" 'Docker.*Compose.*curl.*jq.*Ruby.*Go.*可用'
require_text "$release_notes" 'CGO_ENABLED=0 go install github\.com/yyl1212/agent-studio/cmd/agent-studio@v0\.5\.0-rc\.1'
require_text "$release_notes" 'VERSION=v0\.5\.0-rc\.1'
require_text "$release_notes" 'darwin_amd64'
require_text "$release_notes" 'darwin_arm64'
require_text "$release_notes" 'linux_amd64'
require_text "$release_notes" 'linux_arm64'
require_text "$release_notes" 'checksum|checksums'
require_text "$release_notes" 'SPDX.*SBOM|SBOM.*SPDX'
require_text "$release_notes" '单租户'
require_text "$release_notes" '本地优先'
require_text "$release_notes" '无容器制品'
require_text "$release_notes" '无本地 RAG'
require_text "$release_notes" '无自动业务重试'
require_text "$release_notes" '无签名/公证'
require_text "$release_notes" '升级前.*备份'
require_text "$release_notes" '旧制品'
require_text "$release_notes" '禁止.*逆迁移'
require_text "$release_notes" '禁止.*混跑'

require_text README.md 'v0\.5\.0-rc\.1'
require_text README.md '0\.5\.0-dev'
require_text README.md 'SDK 当前为 v0\.5'
require_text README.md 'agent-studio\.dev/v1alpha1'
require_text README.md 'API.*Worker|Worker.*API'
require_text README.md '队列采样|queue sampler'
require_text README.md '500 Mock runs'
require_text README.md '不是 SLA'
require_text README.md '升级.*回滚|回滚.*升级'
require_text README.md '隔离.*非生产|非生产.*隔离'
require_text README.md '专用.*测试密钥|测试密钥.*专用'
require_text README.md 'RUN_PAYLOAD_ENCRYPTION_KEY.*openssl rand -base64 32'
require_text README.md 'Docker.*Compose.*curl.*jq.*Ruby.*Go.*可用'

require_text docs/sdk/compatibility.md 'Go SDK v0\.5'
require_text docs/sdk/compatibility.md 'agent-studio\.dev/v1alpha1'
require_text docs/sdk/compatibility.md '\[v0\.2\.0, v0\.6\.0\)'
require_text docs/sdk/compatibility.md 'durable execution|耐久运行'
require_text docs/sdk/compatibility.md 'backup schema|备份 schema|备份格式'
require_text docs/sdk/compatibility.md 'runtime 状态|运行时状态'
require_text docs/sdk/compatibility.md '不自动改变.*Node API.*生命周期|Node API.*生命周期.*不自动改变'
require_text docs/sdk/compatibility.md '当前 v0\.5 边界内.*Capability.*展示.*审计.*声明元数据'
require_text docs/sdk/compatibility.md 'Capability.*不是.*权限授予.*沙箱'
if grep -Eq 'Capability.*v0\.4|v0\.4.*Capability' docs/sdk/compatibility.md; then
  printf '%s\n' 'compatibility policy must describe the current v0.5 Capability boundary' >&2
  exit 1
fi

require_text docs/upgrades/v0.5-d.md 'v0\.5\.0-rc\.1'
require_text docs/upgrades/v0.5-d.md '版本矩阵'
require_text docs/upgrades/v0.5-d.md '官方 manifest Runtime 范围.*\[v0\.2\.0, v0\.6\.0\)'
require_text docs/upgrades/v0.5-d.md 'make test-v05-upgrade-rollback-e2e'
require_text docs/upgrades/v0.5-d.md '10 分钟'
require_text docs/upgrades/v0.5-d.md 'v1alpha2.*v1alpha1|v1alpha1.*v1alpha2'
require_text docs/upgrades/v0.5-d.md '升级前.*dump|升级前.*数据库备份'

for document in "$release_notes" README.md docs/sdk/compatibility.md docs/upgrades/v0.5-d.md; do
  if grep -Eq '已发布|已公开|Latest' "$document"; then
    printf '%s\n' "forbidden remote release status in $document" >&2
    exit 1
  fi
done

printf '%s\n' 'durable run documentation contract passed'
