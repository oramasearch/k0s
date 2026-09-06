#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly script_dir
repository_dir="$(cd -- "$script_dir/../.." && pwd)"
readonly repository_dir
readonly installed_k0s="/usr/local/bin/k0s"
readonly k0s_config="/etc/k0s/lake-on-k0s.yaml"
readonly operator_namespace="orama-system"
readonly lake_namespace="lake-dev"
readonly pull_secret="oramasearch-dockerhub"
readonly cnpg_version="${CNPG_CHART_VERSION:-0.29.0}"
readonly wait_timeout="${WAIT_TIMEOUT:-15m}"

use_pull_secret=false

fail() {
  printf 'lake-on-k0s: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

check_environment() {
  [ "$(uname -s)" = Linux ] || fail "this installer must run on Linux"
  [ "${EUID:-$(id -u)}" -eq 0 ] || fail "run this installer as root"
  [ -n "${OPERATOR_CHART:-}" ] || fail "set OPERATOR_CHART to the orama-operator chart directory"
  [ -f "$OPERATOR_CHART/Chart.yaml" ] || fail "Chart.yaml not found under OPERATOR_CHART: $OPERATOR_CHART"
  [ ! -e /var/lib/k0s ] || fail "/var/lib/k0s already exists; use a fresh host or remove the existing cluster deliberately"

  require_command curl
  require_command helm
  require_command install
  if [ -z "${K0S_BINARY:-}" ]; then
    require_command go
    require_command make
  fi
}

anonymous_manifest_available() {
  local image_repository="$1"
  local image_tag="$2"
  local registry_token
  local response_code

  registry_token="$(
    curl -fsS "https://auth.docker.io/token?service=registry.docker.io&scope=repository:${image_repository}:pull" |
      sed -n 's/.*"token":"\([^"]*\)".*/\1/p'
  )"
  [ -n "$registry_token" ] || return 1

  response_code="$(
    curl -sS \
      -o /dev/null \
      -w '%{http_code}' \
      -H "Authorization: Bearer $registry_token" \
      -H 'Accept: application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json' \
      "https://registry-1.docker.io/v2/${image_repository}/manifests/${image_tag}"
  )"
  [ "$response_code" = 200 ]
}

check_registry_access() {
  local operator_public=false
  local lake_public=false

  if anonymous_manifest_available oramasearch/orama-operator v1-sandbox; then
    operator_public=true
  fi
  if anonymous_manifest_available oramasearch/lake v1-sandbox; then
    lake_public=true
  fi
  if "$operator_public" && "$lake_public"; then
    printf '%s\n' "Both images are public; skipping the Docker Hub pull Secret."
    return
  fi

  [ -n "${ORAMA_DOCKERHUB_USERNAME:-}" ] || fail "set ORAMA_DOCKERHUB_USERNAME for the private images"
  [ -n "${ORAMA_DOCKERHUB_TOKEN:-}" ] || fail "set ORAMA_DOCKERHUB_TOKEN to a read-only Docker Hub token"
  use_pull_secret=true
}

build_or_select_k0s() {
  local source_binary

  if [ -n "${K0S_BINARY:-}" ]; then
    source_binary="$K0S_BINARY"
  else
    (
      cd -- "$repository_dir"
      make DOCKER="" EMBEDDED_BINS_BUILDMODE=none BUILD_GO_LDFLAGS_EXTRA="" build
    )
    source_binary="$repository_dir/k0s"
  fi

  [ -x "$source_binary" ] || fail "k0s binary is not executable: $source_binary"
  install -m 0755 "$source_binary" "$installed_k0s"
}

start_k0s() {
  install -d -m 0755 "$(dirname -- "$k0s_config")"
  "$installed_k0s" config create >"$k0s_config"
  "$installed_k0s" install controller \
    --config="$k0s_config" \
    --enable-worker \
    --no-taints \
    --start
}

wait_for_api() {
  local deadline=$((SECONDS + 300))

  until "$installed_k0s" kubectl get --raw=/readyz >/dev/null 2>&1; do
    [ "$SECONDS" -lt "$deadline" ] || fail "the Kubernetes API did not become ready"
    sleep 5
  done
}

wait_for_components() {
  local deadline=$((SECONDS + 900))
  local component_output

  until component_output="$("$installed_k0s" liquid components 2>/dev/null)" &&
    awk 'NR > 1 && $5 != "deployed" && $5 != "external" && $5 != "disabled" { exit 1 }' <<<"$component_output"; do
    [ "$SECONDS" -lt "$deadline" ] || fail "the liquid components did not finish deploying"
    sleep 10
  done

  printf '%s\n' "$component_output"
  "$installed_k0s" kubectl wait \
    --for=condition=Ready \
    node \
    --all \
    --timeout="$wait_timeout"
}

ensure_namespace() {
  "$installed_k0s" kubectl create namespace "$1" \
    --dry-run=client \
    -o yaml |
    "$installed_k0s" kubectl apply -f -
}

create_pull_secret() {
  local namespace="$1"

  "$installed_k0s" kubectl create secret docker-registry "$pull_secret" \
    --namespace="$namespace" \
    --docker-server=https://index.docker.io/v1/ \
    --docker-username="$ORAMA_DOCKERHUB_USERNAME" \
    --docker-password="$ORAMA_DOCKERHUB_TOKEN" \
    --dry-run=client \
    -o yaml |
    "$installed_k0s" kubectl apply -f - >/dev/null
}

prepare_namespaces() {
  ensure_namespace "$operator_namespace"
  ensure_namespace "$lake_namespace"

  if "$use_pull_secret"; then
    create_pull_secret "$operator_namespace"
    create_pull_secret "$lake_namespace"
  fi
}

install_cloudnative_pg() {
  helm repo add cnpg https://cloudnative-pg.github.io/charts --force-update
  helm repo update cnpg
  helm upgrade --install cloudnative-pg cnpg/cloudnative-pg \
    --namespace cnpg-system \
    --create-namespace \
    --version "$cnpg_version" \
    --wait \
    --timeout "$wait_timeout"
}

install_orama_operator() {
  local -a helm_arguments

  helm_arguments=(
    upgrade
    --install
    orama-operator
    "$OPERATOR_CHART"
    --namespace
    "$operator_namespace"
    --values
    "$script_dir/operator-values.yaml"
    --wait
    --timeout
    "$wait_timeout"
  )
  if ! "$use_pull_secret"; then
    helm_arguments+=(--set-json 'operator.imagePullSecrets=[]')
  fi

  helm "${helm_arguments[@]}"
}

apply_lake() {
  if "$use_pull_secret"; then
    "$installed_k0s" kubectl apply -f "$script_dir/lake.yaml"
  else
    sed '/^[[:space:]]*pullSecretName:/d' "$script_dir/lake.yaml" |
      "$installed_k0s" kubectl apply -f -
  fi

  "$installed_k0s" kubectl apply -f "$script_dir/route.yaml"
}

wait_for_lake() {
  "$installed_k0s" kubectl wait \
    --namespace="$lake_namespace" \
    --for=condition=Ready \
    cluster.postgresql.cnpg.io/lake-hello-controldb \
    --timeout="$wait_timeout"
  "$installed_k0s" kubectl rollout status \
    --namespace="$lake_namespace" \
    deployment/lake-hello \
    --timeout="$wait_timeout"
  "$installed_k0s" kubectl wait \
    --namespace=liquid-system \
    --for=condition=Programmed \
    gateway/lake-on-k0s \
    --timeout="$wait_timeout"
}

poll_lake_route() {
  local node_address="${NODE_ADDRESS:-127.0.0.1}"
  local node_port
  local endpoint
  local deadline=$((SECONDS + 300))
  local response

  node_port="$(
    "$installed_k0s" kubectl get service cilium-gateway-lake-on-k0s \
      --namespace=liquid-system \
      -o 'jsonpath={.spec.ports[?(@.port==80)].nodePort}'
  )"
  [ -n "$node_port" ] || fail "the Cilium Gateway has no HTTP NodePort"
  endpoint="http://${node_address}:${node_port}/"

  until response="$(curl -fsS "$endpoint")"; do
    [ "$SECONDS" -lt "$deadline" ] || fail "the Lake did not answer at $endpoint"
    sleep 5
  done

  printf 'Lake answered at %s\n' "$endpoint"
  printf '%s\n' "$response"
}

main() {
  [ "$#" -eq 0 ] || fail "usage: $0"
  check_environment
  check_registry_access
  build_or_select_k0s
  start_k0s
  wait_for_api
  wait_for_components
  prepare_namespaces
  export KUBECONFIG=/var/lib/k0s/pki/admin.conf
  install_cloudnative_pg
  install_orama_operator
  apply_lake
  wait_for_lake
  poll_lake_route
}

main "$@"
