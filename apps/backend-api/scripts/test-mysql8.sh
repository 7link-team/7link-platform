#!/usr/bin/env bash
set -euo pipefail

if (( $# < 3 )); then
  echo "usage: test-mysql8.sh <docker-test-host> <mysql-image> <docker-command...>" >&2
  exit 2
fi

docker_test_host=$1
mysql_image=$2
shift 2
docker_command=("$@")

mysql_database=sevenlink_iam_test
mysql_user=sevenlink_test
mysql_root_password="$(od -An -N24 -tx1 /dev/urandom | tr -d '[:space:]')"
mysql_password="$(od -An -N24 -tx1 /dev/urandom | tr -d '[:space:]')"
mysql_sql_mode=ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION
label_key=com.7link.mysql8-gate
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$-${RANDOM}"
container_name="sevenlink-mysql8-${run_id}"
container_id=

cleanup() {
  local exit_code=$?
  trap - EXIT
  if ! "${docker_command[@]}" rm --force "${container_id:-${container_name}}" >/dev/null 2>&1; then
    if (( exit_code == 0 )); then
      echo "failed to remove MySQL gate container ${container_id:-${container_name}}" >&2
      exit_code=1
    fi
  fi
  exit "${exit_code}"
}
trap cleanup EXIT

container_id="$("${docker_command[@]}" run --detach \
  --name "${container_name}" \
  --label "${label_key}=${run_id}" \
  --env "MYSQL_ROOT_PASSWORD=${mysql_root_password}" \
  --env "MYSQL_DATABASE=${mysql_database}" \
  --env "MYSQL_USER=${mysql_user}" \
  --env "MYSQL_PASSWORD=${mysql_password}" \
  --publish "${docker_test_host}::3306" \
  "${mysql_image}" \
  "--sql-mode=${mysql_sql_mode}" \
  --log-bin-trust-function-creators=ON \
  --character-set-server=utf8mb4 \
  --collation-server=utf8mb4_0900_ai_ci)"

ready=false
for _ in {1..90}; do
  if "${docker_command[@]}" exec "${container_id}" mysqladmin ping --host=127.0.0.1 "--user=${mysql_user}" "--password=${mysql_password}" --silent >/dev/null 2>&1; then
    ready=true
    break
  fi
  if [[ "$("${docker_command[@]}" inspect --format={{.State.Running}} "${container_id}")" != true ]]; then
    break
  fi
  sleep 1
done
if [[ "${ready}" != true ]]; then
  "${docker_command[@]}" logs "${container_id}" >&2
  echo "MySQL container did not become ready" >&2
  exit 1
fi

binding="$("${docker_command[@]}" port "${container_id}" 3306/tcp | head -n 1)"
mysql_port=${binding##*:}
if [[ ! "${mysql_port}" =~ ^[0-9]+$ ]]; then
  echo "cannot parse MySQL port from Docker binding: ${binding}" >&2
  exit 1
fi

echo "mysql8 gate: ${docker_test_host}:${mysql_port} (${mysql_image})"
MYSQL8_DSN="${mysql_user}:${mysql_password}@tcp(${docker_test_host}:${mysql_port})/${mysql_database}?parseTime=true&multiStatements=true&charset=utf8mb4" \
MYSQL8_EXPECTED_SQL_MODE="${mysql_sql_mode}" \
  go -C tests test ./migration -run '^TestIAMMigrationsOnRealMySQL8$' -count=1
