# Dashboard Role Navigation Matrix Handoff

## Status

- Implemented on `codex/role-navigation-matrix` on 2026-08-12.
- Base: `weibao/main@d81dff7f7e04a43af3ff8b12a0b500d862d864d5`.
- This change has not been deployed by this branch.

## Goal

Use one product-page visibility contract for the sidebar, direct dashboard URLs,
and fallback navigation. The contract is based on the built-in role matrix from
the approved product sheet, while existing permissions remain the API
authorization layer.

## Built-in Role Matrix

### Company Workspace

| Page | Super/Admin | Tenant Admin | Team Leader | Agent | Store Staff |
| --- | --- | --- | --- | --- | --- |
| Overview | yes | yes | yes | yes | no |
| Service analytics | yes | yes | yes | no | no |
| Conversations | yes | yes | yes | yes | yes |
| Conversation dispatch | yes | yes | yes | no | no |
| Conversation records | yes | yes | yes | yes | no |
| Tickets | yes | yes | yes | yes | no |
| Customers | yes | yes | yes | yes | no |
| Model billing | yes | yes | yes | no | yes |

### Customer Service Organization

| Page | Super/Admin | Tenant Admin | Team Leader | Agent | Store Staff |
| --- | --- | --- | --- | --- | --- |
| Stores | yes | yes | yes | yes | no |
| Store workbench | yes | no | no | no | yes |
| Agents | yes | yes | yes | yes | no |
| Team schedules | yes | yes | yes | no | no |
| WxWork protocol instances | yes | yes | yes | no | no |
| Arrival connections | yes | yes | yes | yes | no |

### Service, Access, And Platform Areas

- Tenant admins can use all service capability and access-management pages.
- Team leaders can use only quick replies and tags in service capabilities.
- Agents and store staff cannot use service capability or access-management pages.
- Platform management is limited to `super_admin` and `admin` platform accounts.
- Multiple built-in roles receive the union of their built-in page scopes.
- A custom-only role remains permission-driven. When a built-in role is also
  present, the built-in page scope remains the product boundary.

## Shared Guard

`web/lib/navigation.tsx` is the single source for:

- sidebar item filtering;
- direct URL access through `dashboardPathIsAccessible`;
- default redirect through `firstAccessibleDashboardPath`;
- tenant/platform context checks.

`app-sidebar`, `dashboard/layout`, and `dashboard-home` now pass `session.roles`
to the same helpers. A page therefore cannot be restored by manually typing its
URL after it has been removed by the built-in role matrix.

## Retired Product Modules

The following product pages are retired for every role:

- `/dashboard/settings` (`接入设置`);
- `/dashboard/reply-intent-profiles` (`意图行业`).

The server redirects both legacy page paths to `/dashboard/`. Their navigation
entries, translations, static pages, page tests, and dashboard quick links were
removed.

### Channel Boundary

The retired access-settings page previously exposed general Channel CRUD. The
following dashboard management endpoints are no longer registered:

- channel detail, create, update, status update, delete;
- user-token-secret reset;
- legacy WxWork KF account listing.

`/api/dashboard/channel/list` remains because the active WxWork protocol
instance manager uses it to select an existing tenant-scoped protocol channel.
Channel models, services, protocol configuration parsing, and runtime bindings
remain shared infrastructure and were not deleted.

### Intent Profile Boundary

All `/api/dashboard/reply-intent-profile/*` management endpoints and their
handler are retired. The frontend management types and calls were removed.

`ReplyIntentProfile` models, repositories, validation/publish service logic,
seed data, tenant industry binding, and AI Runtime lookups remain internal
dependencies. Removing those would break tenant initialization, intent
classification, industry tags, and the reply runtime.

The retained `意图分类` and `行业标签模板` pages now load minimal Profile
options through the new read-only
`GET /api/dashboard/reply-intent-config/profile_options` endpoint. It requires a
platform account with `aiConfig.view`, returns every non-deleted Profile
(`id`, `code`, `industryCode`, `name`, `revision`, `status`), and does not expose
the retired Profile CRUD, validation, test, or publish operations. Draft and
disabled Profiles remain selectable so administrators can repair their child
intent and tag definitions.

`GET /api/dashboard/tenant/industry_options` remains scoped to platform
accounts with `tenant.view` and only returns fully published, bindable industry
Profiles for company onboarding.

## Permission And Data Changes

The product sheet required four page scopes that existing built-in role data
did not provide:

- `admin`: add `storeWorkbench.view`;
- `cs_team_leader`: add `billing.view`;
- `cs_user`: add `store.view`;
- `cs_user`: add `arrivalConnection.view` and `arrivalAudit.view`.

Migration `76` (`sync role navigation view permissions`) adds these relations
idempotently for existing databases and keeps SQLite/MySQL compatibility. It
does not remove custom role permissions or grant write permissions.

The page matrix intentionally does not rewrite the complete backend operation
permission model. Existing non-page workflow permissions remain untouched to
avoid regressions in conversations, tickets, dispatch, and WxWork processing.

## Interfaces And Compatibility

- Removed: two dashboard pages and their dedicated management HTTP endpoints.
- Added: one platform-only read endpoint for non-deleted Profile option data.
- Preserved: public APIs, WebSocket payloads, AI Runtime contracts, Channel
  runtime contracts, models, database tables, and billing attribution.
- Added: one DML migration; no model or AutoMigrate schema change.
- Frontend static export no longer contains either retired page route.

## Verification

```bash
cd web
node --test lib/navigation.test.mjs components/app-sidebar.test.mjs app/dashboard/layout-permissions.test.mjs
./node_modules/.bin/tsc --noEmit
./node_modules/.bin/next build --webpack

cd ..
go test ./internal/pkg/constants -count=1
go test ./internal/migration -count=1
go test ./internal/bootstrap -run 'NewServerRegistersGinRoutes|NewServerRedirectsRetiredDashboardPages' -count=1
go test ./internal/handlers/dashboard -count=1
go test ./internal/services -run 'Dashboard|Channel|ReplyIntent|TenantIndustry' -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

## Parallel Branch And Merge Notes

- Shared files include `internal/pkg/constants/auth.go`, migration registration,
  `internal/bootstrap/server.go`, `internal/bootstrap/routes.go`,
  `web/lib/navigation.tsx`, `web/app/dashboard/layout.tsx`, and
  `web/lib/api/admin.ts`.
- Active customer-audit and AI branches also modify several of these files.
  Merge them structurally; do not choose an entire side for shared files.
- Preserve migration `76` identity. The original dirty worktree contains an
  unrelated uncommitted migration `78`; this branch does not touch it.
- Recommended order: merge the current mainline first, then this role matrix,
  then manually reconcile later AI/customer-audit work against the shared
  navigation, route, and permission contracts.

## Rollback

Code rollback restores the previous pages and routes. Migration `76` is
additive; a complete data rollback must remove only these built-in relations:

- `cs_team_leader` + `billing.view`;
- `admin` + `storeWorkbench.view`;
- `cs_user` + `store.view`;
- `cs_user` + `arrivalConnection.view`;
- `cs_user` + `arrivalAudit.view`.

Do not delete the permission records themselves because they are shared by
other roles and runtime endpoints.
