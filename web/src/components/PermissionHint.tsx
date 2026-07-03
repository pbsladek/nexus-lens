export function PermissionHint({ allowed, capability }: { allowed: boolean; capability: string }) {
  if (allowed) {
    return null;
  }
  return (
    <p className="run-message">
      Read-only session. Required capability: {capability}.
    </p>
  );
}
