#!/usr/bin/env bash
set -euo pipefail

helper="${1:?queue helper required}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
source "$helper"

queue="$tmp/queue"
mkdir -p "$queue"
jq -n '{id:"kick-1", type:"user.emit", source:"tick", text:"old kick"}' > "$queue/01.json"
jq -n '{id:"person-1", type:"user.emit", source:"person", text:"first message"}' > "$queue/02.json"
jq -n '{id:"kick-2", supersedes:"kick-1", type:"user.emit", text:"middle kick"}' > "$queue/03.json"
jq -n '{id:"person-2", type:"user.emit", source:"person", text:"second message"}' > "$queue/04.json"
jq -n '{id:"kick-3", supersedes:"kick-2", type:"user.emit", text:"new kick"}' > "$queue/05.json"
jq -n '{id:"other", type:"user.emit", text:"other instance"}' > "$queue/06.json"
jq -n '{id:"resume", type:"plect.session.resumed", text:"resumed"}' > "$queue/06b.json"
batch_claim "$queue"
jq -n '{id:"late", type:"user.emit", text:"late message"}' > "$queue/07.json"
batch_render "$queue"
input="$(cat "$queue/.inflight/input")"
[[ "$input" == *"first message"* && "$input" == *"second message"* && "$input" == *"new kick"* && "$input" == *"other instance"* ]]
[[ "$input" != *"old kick"* && "$input" != *"middle kick"* && "$input" != *"late message"* ]]
[[ "$input" == *"[1] id=person-1"* && "$input" == *"[2] id=person-2"* && "$input" == *"[3] id=kick-3"* && "$input" == *"[5] id=resume"* ]]
batch_claim "$queue"
batch_render "$queue"
[[ "$(cat "$queue/.inflight/input")" == "$input" ]]
batch_ack "$queue"
[[ -f "$queue/07.json" && ! -d "$queue/.inflight" ]]
batch_claim "$queue"
batch_render "$queue"
[[ "$(cat "$queue/.inflight/input")" == *"late message"* ]]
batch_ack "$queue"

mkdir -p "$queue"
jq -n '{id:"a", type:"example", text:"one"}' > "$queue/08.json"
jq -n '{id:"b", supersedes:"a", type:"example", text:"two"}' > "$queue/09.json"
jq -n '{id:"c", supersedes:"missing", type:"example", text:"unknown predecessor"}' > "$queue/10.json"
jq -n '{id:"d", type:"github.state", url:"same", text:"snapshot one"}' > "$queue/11.json"
jq -n '{id:"e", type:"github.state", url:"same", text:"snapshot two"}' > "$queue/12.json"
batch_claim "$queue"
batch_render "$queue"
input="$(cat "$queue/.inflight/input")"
[[ "$input" != *"id=a "* && "$input" == *"id=b "* && "$input" == *"unknown predecessor"* ]]
[[ "$input" == *"snapshot one"* && "$input" == *"snapshot two"* ]]
batch_ack "$queue"

mkdir -p "$queue"
jq -n '{id:"retry", type:"example", text:"retry me"}' > "$queue/13.json"
batch_claim "$queue"
batch_failed "$queue" agent 2>/dev/null
first_due="$(cat "$queue/.inflight/retry-after")"
batch_failed "$queue" agent 2>/dev/null
second_due="$(cat "$queue/.inflight/retry-after")"
[[ "$second_due" -gt "$first_due" ]]
batch_failed "$queue" agent 2>/dev/null
[[ ! -d "$queue/.inflight" && -f "$queue/.failed-$(cat "$queue"/.failed-*/batch-id)/13.json" ]]
[[ "$(cat "$queue"/.failed-*/parked-reason)" = agent ]]

non_c_locale="$(locale -a | grep -Ei '^en_US\.(utf8|utf-8)$' | head -1 || true)"
if [ -z "${PLECT_CONFORMANCE_NON_C:-}" ] && [ -n "$non_c_locale" ]; then
    PLECT_CONFORMANCE_NON_C=1 LC_ALL="$non_c_locale" bash "$0" "$helper"
fi
echo "headless queue conformance passed"
