#!/bin/bash
# dupes.sh - Consume all messages from a Kafka topic and report true duplicates
# (same key + same updated timestamp).
#
# Usage:
#   ./dupes.sh              Analyze the 'bank' topic (default)
#   ./dupes.sh canary       Analyze the 'canary' topic
#
# Requires: CLUSTER env var set.

set -euo pipefail

CLUSTER="${CLUSTER:?Set CLUSTER env var}"
ROACHPROD="./bin/roachprod"
KAFKA_NODE=10
TOPIC="${1:-bank}"

echo "Reading all messages from topic '$TOPIC' ..."
messages=$($ROACHPROD ssh "$CLUSTER:$KAFKA_NODE" -- \
  "timeout 15 /mnt/data1/confluent/confluent-*/bin/kafka-console-consumer --bootstrap-server localhost:9092 --topic $TOPIC --from-beginning --property print.key=true --property key.separator=@@@ 2>/dev/null" \
  2>/dev/null || true)

# Filter to data rows only (have "after" field).
data_rows=$(echo "$messages" | grep '"after"')
total=$(echo "$data_rows" | wc -l | tr -d ' ')

echo "Total data messages: $total"
echo ""

# Extract key + updated timestamp pairs and find duplicates.
# A true duplicate is the same key emitted with the same updated timestamp.
echo "True duplicates (same key + same updated timestamp):"
dupes=$(echo "$data_rows" \
  | grep -o '"id": *[0-9]*.*"updated": *"[^"]*"' \
  | sort | uniq -c | sort -rn | awk '$1 > 1')

if [ -z "$dupes" ]; then
  echo "  None found."
else
  echo "$dupes" | while read count pair; do
    echo "  $count x $pair"
  done
fi

# Summary.
unique_pairs=$(echo "$data_rows" | grep -o '"id": *[0-9]*.*"updated": *"[^"]*"' | sort -u | wc -l | tr -d ' ')
dupe_count=$(echo "$dupes" | grep -c . 2>/dev/null || true)
echo ""
echo "Unique (key, timestamp) pairs: $unique_pairs"
echo "Pairs with duplicates: $dupe_count"