#!/bin/sh
set -eu
INTERVAL="${CHANNEL_KNOWLEDGE_REFRESH_INTERVAL_SEC:-180}"
echo "channel-knowledge-refresh: loop every ${INTERVAL}s"
while true; do
  /app/channel-knowledge-refresh \
    || echo "channel-knowledge-refresh: non-zero exit (retrying after sleep)"
  sleep "$INTERVAL"
done
