#!/usr/bin/env bash
# Disposable application compatibility checks; never targets an existing Redis.
set -euo pipefail
cd "$(dirname "$0")/.."
for tool in docker go openssl; do command -v "$tool" >/dev/null; done
redis_test_dir=$(mktemp -d "${TMPDIR:-/tmp}/iam-redis-security.XXXXXX")
redis_test_name="iam-redis-test-$(openssl rand -hex 6)"
cleanup() {
  docker rm -f "$redis_test_name" >/dev/null 2>&1 || true
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
redis_test_arch=$(docker info --format '{{.Architecture}}')
case "$redis_test_arch" in aarch64|arm64) redis_test_arch=arm64;; x86_64|amd64) redis_test_arch=amd64;; *) exit 1;; esac
echo 'Building disposable Linux Redis test runner'
CGO_ENABLED=0 GOOS=linux GOARCH="$redis_test_arch" go test -c -o "$redis_test_dir/authn.test" ./internal/iam/authn
docker run --detach --name "$redis_test_name" --network none --mount "type=bind,src=$redis_test_dir,dst=/work" redis:7.4-alpine sleep 1800 >/dev/null
docker exec -e "IAM_TEST_REDIS_TLS=${IAM_TEST_REDIS_TLS:-0}" "$redis_test_name" sh -eu -c '
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
'
echo 'Redis standalone mTLS/ACL, Cluster, and Sentinel compatibility: PASS'
