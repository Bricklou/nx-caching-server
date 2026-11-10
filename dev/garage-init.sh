#!/usr/bin/env bash
# Creates the layout, the bucket and a fixed access key on the dev Garage; safe to run again.
set -euo pipefail

BUCKET=nx-cache
KEY_ID=GK000000000000000000000001
KEY_SECRET=0000000000000000000000000000000000000000000000000000000000000002

garage() { docker compose -f docker-compose.dev.yml exec -T garage /garage "$@"; }

for _ in $(seq 30); do garage status >/dev/null 2>&1 && break; sleep 1; done
garage status >/dev/null

node=$(garage node id -q | cut -d@ -f1)
garage layout show | grep -q "${node:0:16}" || {
  garage layout assign -z dev -c 1G "$node"
  garage layout apply --version 1
}

garage bucket info "$BUCKET" >/dev/null 2>&1 || garage bucket create "$BUCKET"
garage key info "$KEY_ID" >/dev/null 2>&1 || garage key import --yes -n nx-dev "$KEY_ID" "$KEY_SECRET"
garage bucket allow --read --write --owner "$BUCKET" --key "$KEY_ID"

echo "ready: bucket $BUCKET, key $KEY_ID"
