#!/usr/bin/env bash

batch_claim() {
    local LC_ALL=C queue="$1" name f quarantine
    local dir="$queue/.inflight"
    local -a files=()
    if [ -f "$dir/manifest" ]; then
        mapfile -t files < "$dir/manifest"
    else
        for f in "$queue"/*.json; do
            [ -e "$f" ] || continue
            name="${f##*/}"
            files+=("$name")
        done
        [ "${#files[@]}" -gt 0 ] || return 1
        mkdir -p "$dir"
        printf '%s' "$(date +%s%N)-$$" > "$dir/batch-id" || return 1
        printf '%s\n' "${files[@]}" > "$dir/.manifest.tmp" || return 1
        mv "$dir/.manifest.tmp" "$dir/manifest" || return 1
    fi
    quarantine="$queue.quarantine/$(cat "$dir/batch-id")" || return 1
    for name in "${files[@]}"; do
        if [ ! -e "$dir/$name" ]; then
            [ -f "$quarantine/$name" ] && continue
            [ -e "$queue/$name" ] || return 1
            mv "$queue/$name" "$dir/$name" || return 1
        fi
    done
}

batch_wait_retry() {
    local dir="$1/.inflight" due now
    due="$(cat "$dir/retry-after" 2>/dev/null || true)"
    case "$due" in ''|*[!0-9]*) return 0 ;; esac
    now="$(date +%s)"
    while [ "$now" -lt "$due" ]; do
        sleep 1
        now="$(date +%s)"
    done
    rm -f "$dir/retry-after"
}

batch_failed() {
    local queue="$1" phase="$2" dir="$1/.inflight" count id
    count="$(cat "$dir/$phase-failures" 2>/dev/null || true)"
    case "$count" in ''|*[!0-9]*) count=0 ;; esac
    count=$((count + 1))
    printf '%s' "$count" > "$dir/.$phase-failures.tmp" || return 1
    mv "$dir/.$phase-failures.tmp" "$dir/$phase-failures" || return 1
    id="$(cat "$dir/batch-id")" || return 1
    if [ "$count" -ge 3 ]; then
        printf '%s\n' "$phase" > "$dir/parked-reason" || return 1
        mv "$dir" "$queue/.failed-$id" || return 1
        echo "batch $id parked after $count $phase failures" >&2
        return 0
    fi
    printf '%s' "$(($(date +%s) + count * 2))" > "$dir/.retry-after.tmp" || return 1
    mv "$dir/.retry-after.tmp" "$dir/retry-after" || return 1
    echo "batch $id $phase failure $count/3; retrying after backoff" >&2
}

batch_render() {
    local queue="$1" dir="$1/.inflight" name f id supersedes source type error quarantine
    local n=0
    local -a files=() valid_files=()
    local -A omitted=()
    mapfile -t files < "$dir/manifest"
    quarantine="$queue.quarantine/$(cat "$dir/batch-id")" || return 1
    for name in "${files[@]}"; do
        f="$dir/$name"
        if [ ! -e "$f" ]; then
            [ -f "$quarantine/$name" ] || return 1
            continue
        fi
        if ! error="$(jq -s -e 'length == 1 and (.[0] | type == "object")' "$f" 2>&1 >/dev/null)"; then
            [ -n "$error" ] || error="expected one JSON object"
            error="${error%%$'\n'*}"
            mkdir -p "$quarantine" || return 1
            mv "$f" "$quarantine/$name" || return 1
            printf 'quarantined %s: %s\n' "$f" "$error" >&2
            continue
        fi
        valid_files+=("$name")
        supersedes="$(jq -r '.supersedes // empty' "$f")" || return 1
        [ -n "$supersedes" ] && omitted["$supersedes"]=1
    done
    {
        printf 'Consider and answer every numbered entry in this batch in one turn. Reply once after considering all entries.\n\n'
        for name in "${valid_files[@]}"; do
            f="$dir/$name"
            id="$(jq -r '.id // empty' "$f")" || return 1
            [ -n "$id" ] && [ -n "${omitted[$id]:-}" ] && continue
            [ -n "$id" ] || id="$name"
            type="$(jq -r '.type // empty' "$f")" || return 1
            source="$(jq -r '.source // empty' "$f")" || return 1
            n=$((n + 1))
            printf '[%d] id=%s type=%s source=%s\n' "$n" "$id" "$type" "$source"
            jq -rj '.text // empty' "$f" || return 1
            printf '\n\n'
        done
    } > "$dir/.input.tmp" || return 1
    if [ "$n" -eq 0 ]; then
        : > "$dir/.input.tmp" || return 1
    fi
    mv "$dir/.input.tmp" "$dir/input" || return 1
}

batch_ack() {
    local queue="$1" dir="$1/.inflight" id
    id="$(cat "$dir/batch-id")" || return 1
    mv "$dir" "$queue/.completed-$id" || return 1
    rm -rf "$queue/.completed-$id"
}
