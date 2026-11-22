# shellcheck shell=bash
# Helpers for dispatch/dispatch. Sourced, not run.

die()  { printf '%s: %s\n' "$PROG" "$*" >&2; exit 2; }
say()  { printf '%s %s\n' "$(date '+%F %T')" "$*"; }
vlog() { if [[ "${VERBOSE:-0}" == 1 ]]; then say "$@"; fi; }

# ---------------------------------------------------------------- config
# Sources, highest first: the command line, then dispatch/roles/<role>.conf (the
# dispatcher's own defaults). Inside a role file a review key (review.NAME.KEY) beats
# the role key. Project settings are the run-* wrapper's options; agent rules are role
# cards; message verbs are the diag's.
declare -A CFG_CLI=() CFG_ROLE=()

# Keys whose values are paths, resolved against the role file's directory.
is_path_key() {
  case "${1##*.}" in
    rules|prompt|fix_prompt|retry_prompt|evidence_prompt) return 0 ;;
  esac
  return 1
}

# load_kv FILE ARRAYNAME: KEY=VALUE lines; '#' starts a comment line; blank lines skipped.
load_kv() {
  local file="$1" arr="$2" base line key val
  base=$(CDPATH='' cd -- "$(dirname -- "$file")" && pwd)
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [[ -z "$line" || "$line" == \#* ]] && continue
    [[ "$line" == *=* ]] || { say "WARNING $file: ignored line: $line" >&2; continue; }
    key="${line%%=*}"; val="${line#*=}"
    key="${key%"${key##*[![:space:]]}"}"; val="${val#"${val%%[![:space:]]*}"}"
    if is_path_key "$key" && [[ -n "$val" && "$val" != /* ]]; then val="$base/$val"; fi
    printf -v "${arr}[$key]" '%s' "$val"
  done <"$file"
}

# cfg ROLE KEY DEFAULT [REVIEW]
# The CLI only applies to the role this process was started as, not to reviews it runs.
cfg() {
  local role="$1" key="$2" def="$3" review="${4:-}"
  if [[ -z "$review" && "$role" == "$ROLE" && -n "${CFG_CLI[$key]+x}" ]]; then
    printf '%s\n' "${CFG_CLI[$key]}"; return
  fi
  if [[ -n "$review" && -n "${CFG_ROLE[$ROLE:review.$review.$key]+x}" ]]; then
    printf '%s\n' "${CFG_ROLE[$ROLE:review.$review.$key]}"; return
  fi
  if [[ -n "${CFG_ROLE[$role:$key]+x}" ]]; then printf '%s\n' "${CFG_ROLE[$role:$key]}"; return; fi
  printf '%s\n' "$def"
}

load_role_conf() {
  local role="$1" f="$HERE/roles/$1.conf" tmp k
  [[ -f "$f" ]] || die "no role file $f"
  declare -A _RC=()
  load_kv "$f" _RC
  for k in "${!_RC[@]}"; do CFG_ROLE["$role:$k"]="${_RC[$k]}"; done
  unset _RC tmp
}

# review names, in sorted order, from the executor's role file
review_names() {
  local k
  for k in "${!CFG_ROLE[@]}"; do
    [[ "$k" == "$ROLE:review."*.* ]] && { k="${k#"$ROLE:review."}"; printf '%s\n' "${k%%.*}"; }
  done | sort -u
}

# ------------------------------------------------------------------ diag
# node NAME PATH lines (also inside the /* */ key, where they live).
diag_node_path() {
  awk -v n="$2" '{ l=$0; sub(/^[ \t]+/, "", l)
    if (l ~ /^node[ \t]/) { split(l, a, /[ \t]+/); if (a[2] == n) { print a[3]; exit } } }' "$1"
}

# diag_verb_dests DIAG SRC VERB: every DEST of a 'verb SRC DEST WORD...' line that lists VERB
diag_verb_dests() {
  awk -v s="$2" -v v="$3" '{ l=$0; sub(/^[ \t]+/, "", l)
    if (l ~ /^verb[ \t]/) { n = split(l, a, /[ \t]+/)
      if (a[2] == s) for (i = 4; i <= n; i++) if (a[i] == v) { print a[3]; break } } }' "$1"
}

# body lines of 'message VERB { ... body: { ... } }'
diag_body_lines() {
  awk -v v="$2" '
    { l=$0; sub(/^[ \t]+/, "", l); sub(/[ \t]+$/, "", l) }
    st == 0 && l ~ ("^message[ \t]+" v "[ \t]*[{]") { st = 1; next }
    st == 1 && l ~ /^body:[ \t]*[{]/ { st = 2; next }
    st == 2 { if (l == "}") exit; if (l ~ /^(#|\/\/)/) next; print l }
  ' "$1"
}

# fill_body DIAG VERB OUT   (fields from the assoc array F)
# Literal lines are copied; each 'KEY: <...>' line takes F[KEY] (or 'none').
fill_body() {
  local diag="$1" verb="$2" out="$3" line k v n=0
  local re='^([A-Za-z0-9_-]+):[[:space:]]*<.*>$'
  : >"$out"
  while IFS= read -r line; do
    n=$((n + 1))
    if [[ "$line" =~ $re ]]; then
      k="${BASH_REMATCH[1]}"; v="${F[$k]:-none}"; v="${v//$'\n'/ }"
      printf '%s: %s\n' "$k" "$v" >>"$out"
    else
      printf '%s\n' "$line" >>"$out"
    fi
  done < <(diag_body_lines "$diag" "$verb")
  [[ $n -gt 0 ]]
}

# --------------------------------------------------------------- messages
# parse_message FILE: header FROM/TO/SEQ/VERB into M_*, every other
# 'KEY: value' line (first one wins) into MF.
declare -A MF=() F=()
parse_message() {
  local line k v
  M_FROM=""; M_VERB=""; M_SEQ=""; MF=()
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" =~ ^([A-Za-z0-9_-]+):[[:space:]]?(.*)$ ]] || continue
    k="${BASH_REMATCH[1]}"; v="${BASH_REMATCH[2]}"
    case "$k" in
      FROM) [[ -z "$M_FROM" ]] && M_FROM="$v" ;;
      VERB) [[ -z "$M_VERB" ]] && M_VERB="$v" ;;
      SEQ)  [[ -z "$M_SEQ" ]] && M_SEQ="$v" ;;
      TO|REFS) ;;
      *) [[ -z "${MF[$k]+x}" ]] && MF[$k]="$v" ;;
    esac
  done <"$1"
  return 0
}

# ------------------------------------------------------------ agent output
# last 'STATUS: WORD rest' line of a text file -> "WORD<TAB>rest"
last_status() {
  [[ -f "$1" ]] || return 1
  sed -e 's/\*\*//g' -e 's/`//g' "$1" \
    | awk '{ l=$0; sub(/^[ \t>*-]+/, "", l)
             if (l ~ /^STATUS:[ \t]*[A-Za-z]/) { sub(/^STATUS:[ \t]*/, "", l); s=l } }
           END { if (s == "") exit 1
                 w=s; sub(/[ \t].*$/, "", w); r=s; sub(/^[^ \t]*[ \t]*/, "", r)
                 printf "%s\t%s\n", toupper(w), r }'
}

# ------------------------------------------------------------- templates
# render TEMPLATE OUT  ({{KEY}} -> TV[KEY]; unknown {{...}} left as is)
declare -A TV=()
render() {
  local t k
  t=$(<"$1")
  for k in "${!TV[@]}"; do t="${t//"{{$k}}"/"${TV[$k]}"}"; done
  printf '%s\n' "$t" >"$2"
}

# ------------------------------------------------------------------- git
git_head()  { git -C "$WORKTREE" rev-parse -q --verify HEAD 2>/dev/null || printf 'none\n'; }
git_dirty() { git -C "$WORKTREE" status --porcelain 2>/dev/null | sha256sum | cut -c1-16; }
# -q: a missing tag prints nothing and exits 1; that is "no commit", not an error.
tag_commit() { git -C "$WORKTREE" rev-parse -q --verify "refs/tags/$1^{commit}" || return 0; }
