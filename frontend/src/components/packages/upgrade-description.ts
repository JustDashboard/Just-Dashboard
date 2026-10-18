/** Match the native command each manager runs, including the stricter APT security plan. */
export function upgradeDescription(manager: string | undefined, securityOnly: boolean): string {
  switch (manager) {
    case "apt":
      return securityOnly
        ? "APT is restricted to the security pocket's installed-package candidates. The simulated transaction must not install new packages, remove packages, or change packages outside those candidates."
        : "APT upgrades installed packages and holds back updates that require new packages or removals."
    case "dnf":
    case "yum":
      return `${manager} runs ${securityOnly ? "a security-filtered upgrade" : "an upgrade"}. Its dependency solver may install new dependencies and replace obsolete packages as part of the transaction.`
    case "zypper":
      return `zypper runs ${securityOnly ? "security patches" : "an update"}. Its dependency solver may add or replace packages as part of the transaction.`
    case "pacman":
      return "pacman refreshes the repositories and upgrades the full system. The transaction may install dependencies or replace and remove packages."
    case "apk":
      return "apk reads a fresh repository index and upgrades packages. The transaction may add dependencies or replace packages."
    default:
      return "The host's package manager resolves the transaction. It may install dependencies or replace and remove packages."
  }
}
