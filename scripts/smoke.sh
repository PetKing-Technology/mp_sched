#!/usr/bin/env bash
set -euo pipefail
BASE="${BASE_URL:-http://127.0.0.1:8080}"
P="${API_PREFIX:-/api/sched/v1}"
echo "GET ${BASE}${P}/healthz"
curl -sf "${BASE}${P}/healthz" | head -c 200
echo
echo
echo "POST start task (docker)"
R=$(curl -sf -X POST "${BASE}${P}/tasks" \
  -H "Content-Type: application/json" \
  -d '{"operation":"start","task_class":"fast","provider":"docker","res_cpu":"1","res_memory":"512Mi","business":{"image":"alpine:3.20","command":["sleep","30"]}}')
echo "$R"
TID=$(echo "$R" | sed -n 's/.*"task_id":"\([^"]*\)".*/\1/p' | head -1)
if [[ -n "${TID}" ]]; then
  echo
  echo "GET ${P}/tasks/${TID}"
  curl -sf "${BASE}${P}/tasks/${TID}" | head -c 400
  echo
fi
echo
echo "GET ${P}/tasks?limit=5"
curl -sf "${BASE}${P}/tasks?limit=5" | head -c 400
echo
echo "smoke ok"
