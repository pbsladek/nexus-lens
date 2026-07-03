export function StatusPill({ label, status }: { label: string; status: string }) {
  return <span className={`status-pill ${statusClass(status)}`}>{label}: {status}</span>;
}

export function statusClass(status: string): string {
  const normalized = status.toLowerCase();
  if (
    normalized.includes("failed") ||
    normalized.includes("unreachable") ||
    normalized.includes("invalid") ||
    normalized.includes("blocked")
  ) {
    return "status-bad";
  }
  if (
    normalized.includes("loading") ||
    normalized.includes("checking") ||
    normalized.includes("starting") ||
    normalized.includes("running") ||
    normalized.includes("requesting") ||
    normalized.includes("signing")
  ) {
    return "status-work";
  }
  if (
    normalized.includes("ok") ||
    normalized.includes("loaded") ||
    normalized.includes("signed in") ||
    normalized.includes("saved") ||
    normalized.includes("active")
  ) {
    return "status-good";
  }
  return "status-neutral";
}
