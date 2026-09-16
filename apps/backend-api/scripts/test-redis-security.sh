#!/usr/bin/env bash
# Disposable application compatibility checks; never targets an existing Redis.
#
# The Docker command is a parameter because the daemon is not always local: a
# machine that drives a remote daemon passes its transport here, the same way
# test-mysql8.sh does. Everything that has to reach the daemon therefore travels
# on stdin -- the fixture directory as a tar stream, the container script as
# input to sh. A remote transport puts a shell between here and the daemon, and
# that shell re-splits an argument containing spaces and quotes. Nothing
# re-splits stdin.
set -euo pipefail
cd "$(dirname "$0")/.."

docker_command=("$@")
if (( ${#docker_command[@]} == 0 )); then
  docker_command=(docker)
fi

for tool in go openssl tar; do command -v "$tool" >/dev/null; done
# On Git Bash, openssl is a native Windows binary, so MSYS rewrites any
# argument that looks like a Unix path -- turning -subj /CN=x into a drive
# path and failing the certificate. Excluding that one prefix leaves the
# file paths converted, which the same binary needs. Unix shells ignore it.
export MSYS2_ARG_CONV_EXCL='/CN='
redis_test_dir=$(mktemp -d "${TMPDIR:-/tmp}/iam-redis-security.XXXXXX")
redis_test_name="iam-redis-test-$(openssl rand -hex 6)"
cleanup() {
  "${docker_command[@]}" rm --force "$redis_test_name" >/dev/null 2>&1 || true
  # Exact directory created by this invocation; contains only generated fixtures.
  rm -rf -- "$redis_test_dir"
}
trap cleanup EXIT INT TERM
openssl rand -hex 24 > "$redis_test_dir/data-password"
openssl rand -hex 24 > "$redis_test_dir/sentinel-password"
chmod 600 "$redis_test_dir/"*-password
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$redis_test_dir/ca.key" -out "$redis_test_dir/ca.crt" -days 1 -subj /CN=DisposableRedisCA >/dev/null 2>&1
for identity in server client; do
  openssl req -newkey rsa:2048 -nodes -keyout "$redis_test_dir/$identity.key" -out "$redis_test_dir/$identity.csr" -subj "/CN=$identity" >/dev/null 2>&1
  printf 'subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth,clientAuth\n' > "$redis_test_dir/$identity.ext"
  openssl x509 -req -in "$redis_test_dir/$identity.csr" -CA "$redis_test_dir/ca.crt" -CAkey "$redis_test_dir/ca.key" -CAcreateserial -out "$redis_test_dir/$identity.crt" -days 1 -extfile "$redis_test_dir/$identity.ext" >/dev/null 2>&1
done
# Native Windows openssl writes CRLF, both into a redirected stream and into
# its own -out files. The container reads them as Unix text, where a stray CR
# survives inside a password and the ACL stops matching what redis-cli sends.
# Normalizing here, before the test binary is built into the same directory,
# keeps the rewrite away from anything that is not text. cat preserves the
# passwords' mode where a move would not.
for fixture in "$redis_test_dir"/*; do
  tr -d '\r' < "$fixture" > "$fixture.lf" && cat "$fixture.lf" > "$fixture" && rm -f "$fixture.lf"
done
# The container runs wherever the daemon is, so the test binary is built for
# that machine's architecture rather than this one's.
redis_test_arch=$("${docker_command[@]}" info --format={{.Architecture}} | tr -d '\r')
case "$redis_test_arch" in aarch64|arm64) redis_test_arch=arm64;; x86_64|amd64) redis_test_arch=amd64;; *) exit 1;; esac
echo 'Building disposable Linux Redis test runner'
CGO_ENABLED=0 GOOS=linux GOARCH="$redis_test_arch" go test -c -o "$redis_test_dir/authn.test" ./internal/iam/authn
"${docker_command[@]}" run --detach --name "$redis_test_name" --network none redis:7.4-alpine sleep 1800 >/dev/null
# A bind mount would name a path on the daemon's machine, which holds none of
# these fixtures. The tar stream carries them instead.
"${docker_command[@]}" exec "$redis_test_name" mkdir -p /work
tar -C "$redis_test_dir" -cf - . | "${docker_command[@]}" cp - "$redis_test_name:/work"
# A bind mount used to hand the runner to the container already executable.
# tar carries the mode it was built with instead, and a Windows filesystem has
# no execute bit to carry, so the container grants it.
"${docker_command[@]}" exec "$redis_test_name" chmod +x /work/authn.test
"${docker_command[@]}" exec --interactive -e "IAM_TEST_REDIS_TLS=${IAM_TEST_REDIS_TLS:-0}" "$redis_test_name" sh -eu <<'REDIS_CONTAINER_SCRIPT'
  data_password=$(cat /work/data-password)
  sentinel_password=$(cat /work/sentinel-password)
  redis_cli() {
    if [ "$IAM_TEST_REDIS_TLS" = 1 ]; then
      redis-cli --tls --cert /work/client.crt --key /work/client.key --cacert /work/ca.crt "$@"
    else
      redis-cli "$@"
    fi
  }
  tls_config() {
    printf "port 0\ntls-port %s\ntls-cert-file /work/server.crt\ntls-key-file /work/server.key\ntls-ca-cert-file /work/ca.crt\ntls-auth-clients yes\n" "$1"
  }
  for port in 7000 7001 7002 7003 7004 7005 6380 6381 6382 6400; do
    mkdir -p /work/node-$port
    {
      printf "bind 127.0.0.1\nport %s\ndaemonize yes\ndir /work/node-%s\nlogfile /work/node-%s/redis.log\n" "$port" "$port" "$port"
      printf "user default off\nuser application on >%s ~* &* +@all\nmasteruser application\nmasterauth %s\n" "$data_password" "$data_password"
      if [ "$port" -ge 7000 ]; then
        printf "cluster-enabled yes\ncluster-node-timeout 1000\ncluster-announce-ip 127.0.0.1\n"
        if [ "$IAM_TEST_REDIS_TLS" = 1 ]; then printf "tls-cluster yes\n"; fi
      elif [ "$port" = 6381 ] || [ "$port" = 6382 ]; then
        printf "replicaof 127.0.0.1 6380\n"
      fi
      if [ "$IAM_TEST_REDIS_TLS" = 1 ]; then printf "tls-replication yes\n"; fi
      if [ "$port" = 6400 ] || [ "$IAM_TEST_REDIS_TLS" = 1 ]; then tls_config "$port"; fi
    } > /work/node-$port/redis.conf
    redis-server /work/node-$port/redis.conf
  done
  export REDISCLI_AUTH="$data_password"
  redis_cli --user application --cluster create 127.0.0.1:7000 127.0.0.1:7001 127.0.0.1:7002 127.0.0.1:7003 127.0.0.1:7004 127.0.0.1:7005 --cluster-replicas 1 --cluster-yes
  for port in 26379 26380 26381; do
    mkdir -p /work/node-$port
    {
      printf "bind 127.0.0.1\nport %s\ndaemonize yes\ndir /work/node-%s\nlogfile /work/node-%s/sentinel.log\n" "$port" "$port" "$port"
      printf "user default off\nuser discovery on >%s ~* &* +@all\n" "$sentinel_password"
      printf "sentinel sentinel-user discovery\nsentinel sentinel-pass %s\n" "$sentinel_password"
      printf "sentinel monitor primary 127.0.0.1 6380 2\nsentinel auth-user primary application\nsentinel auth-pass primary %s\n" "$data_password"
      printf "sentinel down-after-milliseconds primary 1000\nsentinel failover-timeout primary 5000\nsentinel parallel-syncs primary 1\n"
      if [ "$IAM_TEST_REDIS_TLS" = 1 ]; then tls_config "$port"; printf "tls-replication yes\n"; fi
    } > /work/node-$port/sentinel.conf
    redis-server /work/node-$port/sentinel.conf --sentinel
  done
  # Poll concrete readiness, allowing replicas and Sentinel peers to discover.
  for attempt in $(seq 1 100); do
    replicas=$(redis_cli --user application -p 6380 info replication | tr -d "\r" | sed -n "s/^connected_slaves://p")
    peers=$(REDISCLI_AUTH="$sentinel_password" redis_cli --user discovery -p 26379 --raw sentinel sentinels primary | grep -c "^name$" || true)
    if [ "$replicas" = 2 ] && [ "$peers" = 2 ]; then break; fi
    sleep 0.1
  done
  IAM_TEST_REDIS_TOPOLOGIES=1 /work/authn.test -test.run "^TestSharedRedisExternal$" -test.v -test.timeout 2m
REDIS_CONTAINER_SCRIPT
echo 'Redis standalone mTLS/ACL, Cluster, and Sentinel compatibility: PASS'
