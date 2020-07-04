#!/bin/bash
set -euo pipefail

export KUBECONFIG="$HOME/.kube/config"
export $(xargs <./development/envfile)

go build ./cmd/oauth-refresher

./oauth-refresher
