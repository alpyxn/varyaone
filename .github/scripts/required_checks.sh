#!/usr/bin/env bash
# Are the checks this workflow produces actually required to merge?
#
# Read-only. It changes nothing; it reports what GitHub is configured to
# enforce, so the answer comes from the repository's settings rather than from
# the presence of a job in ci.yml. A workflow file proves a job runs. It proves
# nothing about whether a red job can be merged past, and the two have drifted
# before: jobs were renamed in a split while the required-check names stayed on
# the old ones, which silently required nothing.
#
#   .github/scripts/required_checks.sh [owner/repo]
#
# Exit codes: 0 = every expected check is required, 1 = something is missing,
# 2 = the answer could not be obtained (no gh, no auth, no permission).
set -euo pipefail

# The checks that must gate a merge. These are job ids from ci.yml; `backend`
# and `frontend` are deliberately kept as the pre-split names other people's
# saved settings and branch rules may still refer to.
EXPECTED="backend backend-generate backend-migrations frontend frontend-e2e containers"

command -v gh >/dev/null 2>&1 || { echo "gh yok; kontrol edilemedi." >&2; exit 2; }
gh auth status >/dev/null 2>&1 || { echo "gh oturumu yok; kontrol edilemedi." >&2; exit 2; }

repo=${1:-$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || true)}
[ -n "$repo" ] || { echo "depo belirlenemedi." >&2; exit 2; }
branch=$(gh repo view "$repo" --json defaultBranchRef -q .defaultBranchRef.name 2>/dev/null || echo main)

echo "Depo:  $repo"
echo "Dal:   $branch"
echo

# Two mechanisms can require a check, and a repository may use either. Both are
# read; "nothing found" is reported as nothing found, never as "fine".
# gh prints the API's error body to stdout, so a 404 would otherwise be
# collected as if it were a check name. Each call is run for its exit status
# first and only its output kept when it succeeded.
api() {
  out=$(gh api "$@" 2>/dev/null) || return 1
  printf '%s\n' "$out"
}

collect_required() {
  api "repos/$repo/rulesets?includes_parents=true" --jq '.[].id' |
    while read -r id; do
      [ -n "$id" ] || continue
      api "repos/$repo/rulesets/$id" \
        --jq '.rules[]? | select(.type=="required_status_checks")
              | .parameters.required_status_checks[]?.context' || true
    done
  api "repos/$repo/branches/$branch/protection" \
    --jq '.required_status_checks.contexts[]?' || true
}

# grep finds nothing when no check is required; that is an answer, not a failure.
required=$(collect_required | grep -v '^$' | sort -u || true)

if [ -z "$required" ]; then
  echo "ZORUNLU KONTROL YOK."
  echo "  Ne bir ruleset ne de dal koruması bu depoda status check zorunlu kılıyor."
  echo "  CI kırmızıyken de merge edilebilir; ci.yml'deki iş adları bunu değiştirmez."
  exit 1
fi

echo "Zorunlu kılınan kontroller:"
printf '  %s\n' $required
echo

missing=""
for check in $EXPECTED; do
  printf '%s\n' "$required" | grep -Fxq "$check" || missing="$missing $check"
done

if [ -n "$missing" ]; then
  echo "EKSİK — bu kontroller zorunlu değil:"
  printf '  %s\n' $missing
  exit 1
fi
echo "Beklenen bütün kontroller zorunlu."
