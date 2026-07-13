const SEEDED_ROLE_LABELS: Record<string, string> = {
  super_admin: "Super admin",
  admin: "Admin",
  tenant_admin: "Company administrator",
  cs_team_leader: "Support team lead",
  cs_user: "Support agent",
  store_staff: "Store staff",
}

export function getRoleDisplayName(
  code: string | undefined,
  fallbackName: string,
  locale: string
) {
  if (locale !== "en-US") {
    return fallbackName
  }
  const label = SEEDED_ROLE_LABELS[code?.trim() ?? ""]
  return label || fallbackName
}
