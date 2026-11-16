#!/usr/bin/env bash

batch_claim() {
    local queue="$1" name f
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
    for name in "${files[@]}"; do
        if [ ! -e "$dir/$name" ]; then
            [ -e "$queue/$name" ] || return 1
            mv "$queue/$name" "$dir/$name" || return 1
        fi
    done
}

batch_render() {
    local queue="$1" dir="$1/.inflight" name f id supersedes source type
    local n=0
    local -a files=()
    local -A omitted=()
    mapfile -t files < "$dir/manifest"
    for name in "${files[@]}"; do
        f="$dir/$name"
        supersedes="$(jq -r '.supersedes // empty' "$f")" || return 1
        [ -n "$supersedes" ] && omitted["$supersedes"]=1
    done
    {
        printf 'Consider and answer every numbered entry in this batch in one turn. Reply once after considering all entries.\n\n'
        for name in "${files[@]}"; do
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
    mv "$dir/.input.tmp" "$dir/input" || return 1
}

batch_ack() {
    local queue="$1" dir="$1/.inflight" id
    id="$(cat "$dir/batch-id")" || return 1
    mv "$dir" "$queue/.completed-$id" || return 1
    rm -rf "$queue/.completed-$id"
}
