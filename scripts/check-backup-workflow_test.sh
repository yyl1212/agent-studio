#!/bin/sh
set -eu

ruby -ryaml -e '
  workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
  job = workflow.fetch("jobs").fetch("backup-recovery")
  raise "wrong name" unless job.fetch("name") == "Backup recovery"
  raise "wrong runner" unless job.fetch("runs-on") == "ubuntu-latest"
  raise "wrong timeout" unless job.fetch("timeout-minutes") == 15
  service = job.fetch("services").fetch("postgres")
  raise "postgres image not pinned" unless service.fetch("image") == "postgres:18"
  env = job.fetch("env")
  raise "CGO disabled missing" unless env.fetch("CGO_ENABLED") == "0"
  steps = job.fetch("steps")
  runs = steps.map { |step| step["run"] }.compact
  raise "backup e2e missing" unless runs.include?("make test-backup-e2e")
  actions = steps.map { |step| step["uses"] }.compact.map { |uses| uses.split("@", 2).first }
  raise "unexpected backup recovery actions: #{actions.inspect}" unless actions == ["actions/checkout", "actions/setup-go"]
  raise "unexpected backup recovery run steps: #{runs.inspect}" unless runs == ["make test-backup-e2e"]
' .github/workflows/ci.yml

ruby -ryaml -e '
  workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
  raise "permissions must be contents: read" unless workflow.fetch("permissions") == {"contents" => "read"}
  job = workflow.fetch("jobs").fetch("backup-recovery")
  url = job.fetch("env").fetch("TEST_DATABASE_URL")
  raise "database URL must use localhost" unless url.include?("@localhost:5432/")
  raise "database URL leaked through a run step" if job.fetch("steps").any? { |step| step.fetch("run", "").include?(url) }
  workflow.fetch("jobs").each_value do |candidate|
    candidate.fetch("steps", []).each do |step|
      uses = step["uses"]
      next unless uses
      raise "action is not pinned by full commit SHA: #{uses}" unless uses.match?(/\A[^@\s]+@[0-9a-f]{40}\z/)
    end
  end
' .github/workflows/ci.yml
