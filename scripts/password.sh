#!/usr/bin/env bash
# The rule auth.ValidatePasswordStrength applies, so the installer can refuse a
# password while the question is still on screen rather than after .env is
# written and the backend has refused to start. C locale because Go counts
# bytes, and because anything outside ASCII is a symbol to it.
# TestShellPasswordRuleMatchesTheBackend holds the two together.
jd_password_problem() {
 local LC_ALL=C pw="$1" classes=0
 if [ "${#pw}" -lt 12 ]; then
  printf 'at least 12 characters\n'
  return 1
 fi
 [[ "$pw" == *[A-Z]* ]] && classes=$((classes + 1))
 [[ "$pw" == *[a-z]* ]] && classes=$((classes + 1))
 [[ "$pw" == *[0-9]* ]] && classes=$((classes + 1))
 [[ "$pw" == *[!A-Za-z0-9]* ]] && classes=$((classes + 1))
 if [ "$classes" -lt 3 ]; then
  printf 'mix at least three of uppercase, lowercase, digits and symbols\n'
  return 1
 fi
 return 0
}
