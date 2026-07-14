import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const [pageSource, teamSidebarSource, teamEditSource, agentEditSource, squadSource] =
  await Promise.all([
    readFile(new URL("./page.tsx", import.meta.url), "utf8"),
    readFile(new URL("./_components/team-sidebar.tsx", import.meta.url), "utf8"),
    readFile(new URL("./_components/team-edit.tsx", import.meta.url), "utf8"),
    readFile(new URL("./_components/edit.tsx", import.meta.url), "utf8"),
    readFile(new URL("./_components/squad-arrangement.tsx", import.meta.url), "utf8"),
  ])

test("agent profile actions use their matching permissions", () => {
  assert.match(pageSource, /permissions\.has\("agent\.create"\)/)
  assert.match(pageSource, /permissions\.has\("agent\.update"\)/)
  assert.match(pageSource, /permissions\.has\("agent\.delete"\)/)
  assert.match(pageSource, /if \(!canCreateAgent\) throw new Error/)
  assert.match(pageSource, /if \(!canUpdateAgent\) throw new Error/)
  assert.match(pageSource, /if \(!canDeleteAgent\) throw new Error/)
  assert.match(pageSource, /showCreate=\{canCreateAgent\}/)
  assert.match(pageSource, /showEdit=\{canUpdateAgent\}/)
  assert.match(
    pageSource,
    /deleteItem=\{canDeleteAgent \? deleteAgentWithPermission : undefined\}/,
  )
  assert.match(pageSource, /showActionsColumn=\{canUpdateAgent \|\| canDeleteAgent\}/)
})

test("agent creation and organization views require auxiliary read access", () => {
  assert.match(pageSource, /permissions\.has\("agentTeam\.view"\)/)
  assert.match(pageSource, /permissions\.has\("user\.view"\)/)
  assert.match(
    pageSource,
    /selectedTeam\?\.manageable[\s\S]*?canViewTeams[\s\S]*?canViewUsers[\s\S]*?permissions\.has\("agent\.create"\)/,
  )
  assert.match(pageSource, /\{canViewTeams \? \(\s*<div/)
  assert.match(
    agentEditSource,
    /canViewUsers\s*\? fetchUsersAll\(\{ roleCode: "cs_user" \}\)\s*:\s*Promise\.resolve\(\[\]\)/,
  )
  assert.match(
    agentEditSource,
    /canViewTeams \? fetchAgentTeamsAll\(\) : Promise\.resolve\(\[\]\)/,
  )
  assert.match(agentEditSource, /disabled=\{!canViewUsers\}/)
  assert.match(agentEditSource, /disabled=\{!canViewTeams\}/)
})

test("company supervisors participate in tenant customer service organization", () => {
  assert.match(
    teamSidebarSource,
    /roles\.has\("super_admin"\) \|\| roles\.has\("admin"\) \|\| roles\.has\("tenant_admin"\)/,
  )
  assert.match(teamSidebarSource, /permissions\.has\("agentTeam\.create"\)/)
  assert.match(teamSidebarSource, /if \(!canCreateTeam\)/)
  assert.match(teamSidebarSource, /if \(!canUpdateTeam \|\| !item\.manageable\)/)
  assert.match(teamSidebarSource, /if \(!canDeleteTeam \|\| !item\.manageable\)/)
  assert.match(
    teamSidebarSource,
    /open=\{dialogOpen && \(editingItem \? canUpdateTeam : canCreateTeam\)\}/,
  )
})

test("team user selectors do not load or open without user view permission", () => {
  assert.match(teamEditSource, /if \(!canViewUsers\)/)
  assert.match(
    teamEditSource,
    /fetchUsersAll\(\{ roleCode: "cs_team_leader" \}\)/,
  )
  assert.match(teamEditSource, /fetchUsersAll\(\{ roleCode: "store_staff" \}\)/)
  assert.match(teamEditSource, /disabled=\{!canChangeLeader \|\| !canViewUsers\}/)
  assert.match(teamEditSource, /\{canViewUsers \? <Button/)
  assert.match(teamEditSource, /open=\{staffDialogOpen && canViewUsers\}/)
})

test("squad editing, membership and scheduling keep distinct permission boundaries", () => {
  assert.match(squadSource, /canCreate: boolean/)
  assert.match(squadSource, /canUpdate: boolean/)
  assert.match(squadSource, /canDelete: boolean/)
  assert.match(squadSource, /canSchedule: boolean/)
  assert.match(squadSource, /if \(!canUpdate\)/)
  assert.match(squadSource, /if \(editingSquad \? !canUpdate : !canCreate\)/)
  assert.match(squadSource, /if \(!canDelete\)/)
  assert.match(squadSource, /if \(!canSchedule\)/)
  assert.match(squadSource, /disabled=\{!canUpdate\}/)
  assert.match(squadSource, /\{canCreate \? <Button/)
  assert.match(squadSource, /\{canSchedule \? <Button/)
  assert.match(
    pageSource,
    /permissions\.has\("agentTeamSchedule\.view"\)\s*&&\s*permissions\.has\("agentTeamSchedule\.create"\)/,
  )
})
