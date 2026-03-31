#!/bin/bash
# canary.sh - Interactive canary row tester for bank.canary changefeed.
#
# Table schema: canary (id INT PRIMARY KEY, val STRING NOT NULL)
#
# Starts a long-lived Kafka consumer in the background, then loops prompting
# for rows to upsert. Each upsert is verified against the Kafka stream.
# Re-using an existing id tests UPDATE emission through the changefeed.
#
# Commands at the prompt:
#   <id> <val>           Upsert a single row
#   batch <N>            Prompt for N rows, upsert all at once
#   check                Show all canary rows in the DB
#   quit                 Exit
#
# Requires: CLUSTER env var set.

set -euo pipefail

CLUSTER="${CLUSTER:?Set CLUSTER env var}"
ROACHPROD="./bin/roachprod"
KAFKA_NODE=10
KAFKA_LOG=$(mktemp /tmp/canary-kafka.XXXXX)

run_sql() {
  $ROACHPROD sql "$CLUSTER:1" --secure -- -e "$1"
}

# Start a long-lived Kafka consumer that tails the canary topic into a file.
echo "Starting Kafka consumer (tailing canary topic) ..."
$ROACHPROD ssh "$CLUSTER:$KAFKA_NODE" -- \
  '/mnt/data1/confluent/confluent-*/bin/kafka-console-consumer --bootstrap-server localhost:9092 --topic canary --from-beginning 2>/dev/null' \
  > "$KAFKA_LOG" 2>/dev/null &
KAFKA_PID=$!

cleanup() {
  kill "$KAFKA_PID" 2>/dev/null || true
  rm -f "$KAFKA_LOG"
}
trap cleanup EXIT

# Wait a moment for the consumer to connect.
sleep 3
echo "Kafka consumer running (pid=$KAFKA_PID, log=$KAFKA_LOG)"
echo ""

wait_for_ids() {
  local ids="$1"
  local count="$2"

  echo "Waiting for $count row(s) in Kafka (up to 90s) ..."
  for attempt in $(seq 1 18); do
    local found=0
    for check_id in $ids; do
      if grep -q "\"id\": *${check_id}[,}]" "$KAFKA_LOG" 2>/dev/null; then
        found=$((found + 1))
      fi
    done
    if [ "$found" -ge "$count" ]; then
      echo "Found all $count row(s) in Kafka:"
      for check_id in $ids; do
        local matches
        matches=$(grep -c "\"id\": *${check_id}[,}]" "$KAFKA_LOG" 2>/dev/null || true)
        echo "--- id=$check_id ($matches emissions) ---"
        grep "\"id\": *${check_id}[,}]" "$KAFKA_LOG"
      done
      return 0
    fi
    echo "  $found/$count found, waiting ..."
    sleep 5
  done
  echo "TIMEOUT: not all rows appeared after 90s."
  return 1
}

while true; do
  echo ""
  read -rp "canary> " cmd args || break

  case "$cmd" in
    quit|exit|q)
      break
      ;;
    check)
      run_sql "SELECT id, val FROM bank.canary ORDER BY id"
      ;;
    batch)
      count="${args:?Usage: batch <N>}"
      values=""
      id_list=""
      for i in $(seq 1 "$count"); do
        read -rp "  row $i/$count - id: " id
        read -rp "  row $i/$count - val: " val
        [ -n "$values" ] && values="$values, "
        values="$values($id, '$val')"
        id_list="$id_list $id"
      done
      run_sql "UPSERT INTO bank.canary (id, val) VALUES $values"
      echo "Upserted $count rows."
      wait_for_ids "$id_list" "$count"
      ;;
    "")
      continue
      ;;
    *)
      # Treat as: <id> <val>
      id="$cmd"
      val="$args"
      if [ -z "$val" ]; then
        echo "Usage: <id> <val>, batch <N>, check, or quit"
        continue
      fi
      run_sql "UPSERT INTO bank.canary (id, val) VALUES ($id, '$val')"
      echo "Upserted: id=$id val=$val"
      wait_for_ids "$id" 1
      ;;
  esac
done

echo "Bye."
