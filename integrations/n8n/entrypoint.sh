#!/bin/sh
# Copy the baked-in node package into the shared volume that n8n scans via
# N8N_CUSTOM_EXTENSIONS. Runs as an initContainer on every pod start; the volume is a
# fresh emptyDir, so this is the version of record for that pod. Idempotent.
set -eu

TARGET="${TAKUHAI_NODES_TARGET:-/opt/n8n/custom}"
DEST="${TARGET}/n8n-nodes-takuhai"

echo "takuhai-n8n-nodes: installing into ${DEST}"
mkdir -p "${DEST}"
cp -r /takuhai-nodes/. "${DEST}/"
# World-readable so n8n's runtime user can load the package.
chmod -R a+rX "${DEST}"
echo "takuhai-n8n-nodes: installed"
ls -la "${DEST}"
