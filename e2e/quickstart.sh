#!/usr/bin/env bash
#
# Manage the local standalone Stellar network the e2e suite runs against with
# `make e2e-local`.
#
# Quickstart runs stellar-core, Horizon, Soroban RPC and friendbot in one
# container. In local mode the network's root account is derived from the fixed
# standalone passphrase ("Standalone Network ; February 2017"), so the same
# account exists on every machine. That is what makes a local run reproducible,
# and it lets the suite fund its test accounts by a transfer from the root
# account instead of asking a service — the friendbot-free funding path.
#
# Usage:
#   e2e/quickstart.sh start                 start the container and wait until the RPC answers
#   e2e/quickstart.sh root-seed             print the network's root account secret seed
#   e2e/quickstart.sh passphrase            print the network passphrase the RPC reports
#   e2e/quickstart.sh deploy-native-asset [seed]
#                                           deploy the native XLM asset contract (needs stellar-cli)
#   e2e/quickstart.sh stop                  stop and remove the container
#
# The container name, image and host port can be overridden with
# SOROAUTH_QUICKSTART_CONTAINER, SOROAUTH_QUICKSTART_IMAGE and
# SOROAUTH_QUICKSTART_PORT; the two e2e-local paths in the Makefile set the
# port so the RPC URL and the container agree.
set -euo pipefail

CONTAINER="${SOROAUTH_QUICKSTART_CONTAINER:-soroauth-quickstart}"
IMAGE="${SOROAUTH_QUICKSTART_IMAGE:-stellar/quickstart:latest}"
PORT="${SOROAUTH_QUICKSTART_PORT:-8000}"
RPC_URL="http://localhost:${PORT}/rpc"

log() { printf '%s\n' "$*" >&2; }

require_docker() {
	command -v docker >/dev/null 2>&1 || {
		log "docker is required to run the local Stellar network"
		exit 1
	}
}

# start runs the container if it is not already up, then blocks until the RPC
# answers getNetwork. The wait matters: the container reports running seconds
# before stellar-core has a network for the RPC to serve, and a test run started
# too early fails in a way that looks like a signing bug.
start() {
	require_docker

	if [ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null || true)" = "true" ]; then
		log "quickstart container ${CONTAINER} is already running"
	else
		log "starting ${IMAGE} as ${CONTAINER} on port ${PORT}"
		docker run -d --name "$CONTAINER" -p "${PORT}:8000" "$IMAGE" --local >/dev/null
	fi

	log "waiting for the RPC at ${RPC_URL} to serve a network"
	local attempt
	for attempt in $(seq 1 60); do
		if curl -fsS -X POST -H 'Content-Type: application/json' \
			-d '{"jsonrpc":"2.0","id":1,"method":"getNetwork"}' "$RPC_URL" 2>/dev/null |
			grep -q '"passphrase"'; then
			log "the local network is ready at ${RPC_URL}"
			return 0
		fi
		sleep 5
	done

	log "the RPC at ${RPC_URL} did not become ready; inspect: docker logs ${CONTAINER}"
	return 1
}

# root-seed reads the network's root account secret seed out of the container's
# own startup log rather than hard-coding it. The key is derived from the
# network passphrase, so reading back what the node actually used is the value
# in force, not a copy that can drift from it.
root_seed() {
	require_docker
	local seed
	seed="$(docker logs "$CONTAINER" 2>&1 |
		grep -oE 'network root secret key: S[A-Z2-7]+' |
		tail -n1 |
		awk '{print $NF}')"
	if [ -z "$seed" ]; then
		log "could not read the root secret key from docker logs ${CONTAINER}; is it running?"
		return 1
	fi
	printf '%s\n' "$seed"
}

stop() {
	require_docker
	if docker rm -f "$CONTAINER" >/dev/null 2>&1; then
		log "removed ${CONTAINER}"
	else
		log "${CONTAINER} was not running"
	fi
}

# passphrase prints the passphrase the running RPC reports, so callers that need
# it — deploying the network's native asset contract, say — read the value in
# force rather than hard-coding the standalone default.
passphrase() {
	local value
	value="$(curl -fsS -X POST -H 'Content-Type: application/json' \
		-d '{"jsonrpc":"2.0","id":1,"method":"getNetwork"}' "$RPC_URL" 2>/dev/null |
		sed -n 's/.*"passphrase":"\([^"]*\)".*/\1/p')"
	if [ -z "$value" ]; then
		log "could not read the network passphrase from ${RPC_URL}; is it running?"
		return 1
	fi
	printf '%s\n' "$value"
}

# deploy-native-asset creates the native XLM Stellar Asset Contract on a fresh
# local network.
#
# A public network already has one; a local quickstart does not, and every
# scenario that moves native XLM through a SAC needs it. Deploying it is part of
# preparing a local network, not part of the library under test, which is why it
# lives here rather than in the Go suite. The contract id is deterministic, so
# running this twice finds the same contract already deployed, which is treated
# as success rather than a failure.
deploy_native_asset() {
	require_docker
	command -v stellar >/dev/null 2>&1 || {
		log "stellar-cli is required to deploy the local network's native asset contract"
		exit 1
	}

	local seed="${1:-}"
	if [ -z "$seed" ]; then
		seed="$(root_seed)"
	fi
	local pass
	pass="$(passphrase)"

	local out
	if out="$(stellar contract asset deploy \
		--asset native \
		--source-account "$seed" \
		--rpc-url "$RPC_URL" \
		--network-passphrase "$pass" 2>&1)"; then
		log "deployed the native asset contract for the local network"
		return 0
	fi
	if printf '%s' "$out" | grep -qE 'ExistingValue|already exists'; then
		log "the native asset contract is already deployed"
		return 0
	fi
	log "deploying the native asset contract failed:"
	printf '%s\n' "$out" >&2
	return 1
}

case "${1:-}" in
	start) start ;;
	root-seed) root_seed ;;
	passphrase) passphrase ;;
	deploy-native-asset) deploy_native_asset "${2:-}" ;;
	stop) stop ;;
	*)
		log "usage: $0 {start|root-seed|passphrase|deploy-native-asset [seed]|stop}"
		exit 2
		;;
esac
