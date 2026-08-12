# Customer Service Sidebar Visibility Handoff

## Goal

Hide the complete `nav.serviceCapabilities` and `nav.accessManagement` sidebar sections for a standalone `cs_user` account.

## Behavior

- `cs_user` without an elevated role does not see either section in the sidebar.
- `cs_team_leader`, `tenant_admin`, `admin`, and `super_admin` retain the existing navigation.
- A mixed account containing `cs_user` plus any elevated role retains the elevated navigation.
- Existing permission-based item filtering remains unchanged.

## Scope

- Frontend navigation filtering only.
- No backend permission, role, API, DTO, enum, WebSocket, database, or migration changes.
- Direct-route authorization remains governed by the existing permission contract.

## Files

- `web/lib/navigation.tsx`
- `web/components/app-sidebar.tsx`
- `web/lib/navigation.test.mjs`
- `web/components/app-sidebar.test.mjs`

## Verification

```bash
cd web
node --test lib/navigation.test.mjs components/app-sidebar.test.mjs
pnpm typecheck
git diff --check
```

## Parallel Branch And Rollback

- Based on `weibao/main@702c42d`; `origin/main` was identical when implementation began.
- No shared backend contract changed and no merge ordering dependency was introduced.
- Rollback is limited to reverting the frontend navigation filter and its tests.
