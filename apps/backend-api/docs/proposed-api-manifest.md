# Proposed Canonical API Manifest

Status: PROPOSED - requires explicit confirmation before implementation

Date: 2026-09-23

## Contract Rules

- Paths name business resources, relationships, representations, or protocol
  resources; methods carry actions.
- `GET /me` only discovers the authenticated principal ID.
- Self-service and administration use the same target-ID resources.
- Create/list uses the proven direct owner only when ownership is real;
  globally identified items use a short URI and may also expose a direct-owner
  scoped URI for contextual navigation.
- Each resource has one canonical representation. Filters, field selection,
  content negotiation, and embedded summaries do not create duplicate APIs.
- Snowflake IDs share one schema; opaque credential/session IDs use explicitly
  named opaque schemas.
- All ordinary lists share `limit`, `offset`, `sort`, and documented filters.
  File traversal may retain cursor pagination.
- Mutations use one concurrency contract: `If-Match` for versioned resources.
- OAuth/OIDC, SAML, SCIM, and health probes retain standard paths.

### Dual Item Address Contract

For an approved pair such as `/instances/{instance_id}` and
`/tenants/{tenant_id}/instances/{instance_id}`, `instance_id` is the resource
identity in both cases. The short route loads the instance and derives its
tenant. The scoped route loads the same instance, derives its tenant, and fails
with the standard not-found response when it differs from the path tenant.
Both addresses share schemas, ETag/version, authorization, service execution,
side effects, and audit events.

Dual addresses are opt-in, not generated for every owned resource. The current
proposal retains only the two context-rich pairs explicitly justified by tenant
directory navigation:

| Scoped item address | Short item address |
| --- | --- |
| `/tenants/{tenant_id}/entities/{entity_id}` | `/entities/{entity_id}` |
| `/tenants/{tenant_id}/departments/{department_id}` | `/departments/{department_id}` |

No transitive variants such as tenant plus entity plus a globally identified
department are generated unless that full chain itself is the resource identity.
Groups, positions, roles, menus, memberships, invitations, service accounts,
and application configuration resources keep their owner-scoped collection and
short item URI only. A scoped item form can be added later only with a concrete
navigation or integration requirement.

## Shared Parameters And Responses

| Component | Definition |
| --- | --- |
| `SnowflakeId` | decimal string, `^[1-9][0-9]{0,18}$` |
| `OpaqueId` | resource-specific opaque string; never reused as Snowflake |
| `Limit` | integer, default 50, min 1, max 200 |
| `Offset` | integer, default 0, min 0 |
| `If-Match` | required on updates/deletes where lost updates are possible |
| list response | `items`, `page.limit`, `page.offset`, `page.total` |
| error response | one shared error envelope and stable error code catalog |

## `system`

| Method | URI | Definition |
| --- | --- | --- |
| GET | `/healthz` | Aggregated module health. |
| GET | `/livez` | Process liveness. |
| GET | `/readyz` | Dependency readiness. |

Remove public `GET /guid` and `GET /guid/{count}`. ID allocation remains an
internal capability unless an external consumer is proven.

## `authentication`

| Method | URI | Definition |
| --- | --- | --- |
| GET | `/authentication-methods` | Enabled public login methods; replaces `/login/options`. |
| POST | `/authentication-transactions` | Start password, passkey, or federated authentication and return transaction state/options. |
| GET | `/authentication-transactions/{transaction_id}` | Read the non-secret state and allowed next steps. |
| PATCH | `/authentication-transactions/{transaction_id}` | Submit the next credential/MFA/passkey assertion; successful completion returns the created session. |
| POST | `/registration-challenges` | Create a registration captcha/challenge. |
| POST | `/registrations` | Create a pending principal registration. |
| POST | `/email-verification-challenges` | Issue an email verification credential. |
| POST | `/email-verifications` | Consume an email verification credential. |
| POST | `/password-recovery-challenges` | Issue a recovery credential without account enumeration. |
| POST | `/password-recoveries` | Consume a recovery credential and replace the password. |
| POST | `/invitation-acceptances` | Consume an invitation credential and create/bind identity atomically. |

The transaction resource replaces `/login`, `/login/mfa`,
`/login/passkey/options`, and `/login/passkey/verify` as one state machine.

## `account`

| Method | URI | Definition |
| --- | --- | --- |
| GET | `/me` | Return only the authenticated principal identity/bootstrap representation. |
| GET, POST | `/principals` | List authorized global principals or create one global principal. Membership creation remains explicit. |
| GET, PATCH | `/principals/{principal_id}` | Read or update the global principal profile with field filtering. |
| PUT, DELETE | `/principals/{principal_id}/suspension` | Apply or lift global suspension. |
| GET, PUT | `/principals/{principal_id}/avatar` | Read or replace avatar metadata/content according to media type. |
| POST | `/principals/{principal_id}/avatar-candidates` | Generate private expiring candidates. |
| GET | `/principals/{principal_id}/avatar-candidates/{candidate_id}` | Read one private candidate. |
| PATCH | `/principals/{principal_id}/password` | Replace the principal password after required fresh authentication. |
| GET | `/principals/{principal_id}/mfa` | Read MFA state. |
| POST | `/principals/{principal_id}/mfa/totp-enrollments` | Start TOTP enrollment. |
| PUT, DELETE | `/principals/{principal_id}/mfa/totp` | Confirm or remove the TOTP factor. |
| PUT | `/principals/{principal_id}/mfa/recovery-codes` | Replace recovery codes. |
| GET, POST | `/principals/{principal_id}/passkeys` | List or register passkeys. |
| PATCH, DELETE | `/principals/{principal_id}/passkeys/{passkey_id}` | Rename or remove one passkey. |
| POST | `/principals/{principal_id}/passkey-challenges` | Start passkey registration/fresh verification with a declared purpose. |
| GET, POST | `/principals/{principal_id}/personal-tokens` | List or create personal tokens; creation validates submitted delegation. |
| DELETE | `/principals/{principal_id}/personal-tokens/{token_id}` | Revoke one token. |
| POST | `/principals/{principal_id}/token-preparations` | Bind requested token permissions/expiry to fresh verification. |
| GET | `/principals/{principal_id}/sessions` | List active sessions. |
| DELETE | `/principals/{principal_id}/sessions` | Revoke all sessions. |
| GET, DELETE | `/sessions/{session_id}` | Read or revoke one session, including the current session by returned ID. |
| POST | `/sessions/{session_id}/step-up-challenges` | Start session step-up. |
| POST | `/sessions/{session_id}/step-up-verifications` | Complete session step-up. |
| POST | `/principals/{principal_id}/reauthentication-challenges` | Start fresh verification for a sensitive purpose. |
| POST | `/principals/{principal_id}/reauthentication-verifications` | Complete fresh verification. |
| GET | `/principals/{principal_id}/reauthentication-methods` | List available fresh-verification methods. |
| GET | `/principals/{principal_id}/identity-link-providers` | List providers available for linking. |
| POST | `/principals/{principal_id}/identity-link-transactions` | Start link or linked-identity reauthentication. |
| GET | `/principals/{principal_id}/identity-links` | List linked identities. |
| DELETE | `/principals/{principal_id}/identity-links/{link_id}` | Remove one link while preserving a valid login method. |
| GET | `/principals/{principal_id}/oauth-consents` | List application consents. |
| DELETE | `/principals/{principal_id}/oauth-consents/{client_id}` | Withdraw one application consent. |
| GET, PUT, DELETE | `/principals/{principal_id}/account-closure` | Read, request, or cancel the one-per-principal singleton. |
| GET, POST | `/tenants/{tenant_id}/menus` | List or create administrative menu definitions. |
| GET, PATCH, DELETE | `/menus/{menu_id}` | Read, update, or delete one menu definition. |
| GET | `/menu-definitions` | List menu metadata available for configuration. |

Remove `/me/personal-token-permissions`. Do not add a replacement catalog.

## `directory`

| Method | URI | Definition |
| --- | --- | --- |
| GET, POST | `/tenants` | List or create tenants. |
| GET, PATCH, DELETE | `/tenants/{tenant_id}` | Read, update, suspend, or soft-delete a tenant according to schema/state. |
| GET, POST | `/principals` | Canonical global principal collection, shared with `account`. |
| GET, POST | `/memberships` | Filter by `tenant_id` and/or `principal_id`; create a membership for an existing principal. |
| GET, PATCH, DELETE | `/memberships/{membership_id}` | Read, update, disable/leave, or delete one tenant membership. |
| GET, POST | `/tenants/{tenant_id}/entities` | List or create tenant-owned entities. |
| GET, PATCH, DELETE | `/entities/{entity_id}` | Read, update, or delete one entity. |
| GET, POST | `/tenants/{tenant_id}/departments` | List or create tenant departments; `entity_id` is an optional association/filter. |
| GET, PATCH, DELETE | `/departments/{department_id}` | Read, update/move, or delete one department. |
| GET, POST | `/tenants/{tenant_id}/groups` | List or create groups. |
| GET, PATCH, DELETE | `/groups/{group_id}` | Read, update, or delete a group. |
| GET | `/groups/{group_id}/members` | List group-member relationships. |
| PUT, DELETE | `/groups/{group_id}/members/{principal_id}` | Assign/update or remove one principal relationship. |
| GET, POST | `/tenants/{tenant_id}/positions` | List or create positions. |
| GET, PATCH, DELETE | `/positions/{position_id}` | Read, update, or delete a position. |
| GET | `/positions/{position_id}/members` | List position-member relationships. |
| PUT, DELETE | `/positions/{position_id}/members/{principal_id}` | Assign/update or remove one principal relationship. |
| GET, POST | `/tenants/{tenant_id}/invitations` | List or create invitations. |
| DELETE | `/invitations/{invitation_id}` | Revoke a pending invitation. |
| POST | `/invitations/{invitation_id}/deliveries` | Create a rotated invitation delivery credential. |
| GET, POST | `/tenants/{tenant_id}/service-accounts` | List or create service accounts. |
| GET, PUT, DELETE | `/service-accounts/{service_account_id}` | Read, replace, or delete one service account. |
| GET, POST | `/service-accounts/{service_account_id}/credentials` | List or create credentials. |
| DELETE | `/service-accounts/{service_account_id}/credentials/{credential_id}` | Revoke a credential. |

Recommended membership decision: add global `membership_id`. Membership has
status, tenant-local fields, timestamps, authorization projections, and its own
lifecycle, so treating `principal_id` as its item ID is rejected. Remove the
entire `/tenants/{tenant_id}/principals*` family after migration.

## `access-control`

| Method | URI | Definition |
| --- | --- | --- |
| GET, POST | `/tenants/{tenant_id}/roles` | List or create roles. |
| GET, PATCH, DELETE | `/roles/{role_id}` | Read, update, or delete a role. |
| GET | `/roles/{role_id}/members` | List direct principal assignments. |
| PUT, DELETE | `/roles/{role_id}/members/{principal_id}` | Add or remove a principal assignment. |
| GET | `/roles/{role_id}/permission-grants` | List grants including conditions. |
| PUT, PATCH, DELETE | `/roles/{role_id}/permission-grants/{permission_code}` | Create, update condition, or revoke one grant. |
| GET, PUT | `/roles/{role_id}/data-scope` | Read or replace role data scope. |
| GET | `/roles/{role_id}/group-bindings` | List group assignments. |
| PUT, DELETE | `/roles/{role_id}/group-bindings/{group_id}` | Assign or remove a group. |
| GET | `/roles/{role_id}/position-bindings` | List position assignments. |
| PUT, DELETE | `/roles/{role_id}/position-bindings/{position_id}` | Assign or remove a position. |
| GET | `/entities/{entity_id}/role-bindings` | List direct entity role assignments. |
| PUT, DELETE | `/entities/{entity_id}/role-bindings/{role_id}/{principal_id}` | Assign or remove one composite entity role relationship. |
| GET, POST | `/relationships` | Filter or create authorization relationships. |
| GET, PATCH, DELETE | `/relationships/{relationship_id}` | Read, update, or remove one relationship. |
| GET | `/permission-definitions` | List declared permission definitions; optional `scope=platform|tenant`. |
| GET | `/field-definitions` | List declared field capabilities. |
| GET, POST | `/tenants/{tenant_id}/field-policies` | List or create policies. |
| GET, PUT, DELETE | `/field-policies/{policy_id}` | Read, replace, or restore/delete one policy. |
| GET | `/authorization-model` | Read scope and relationship model metadata. |
| POST | `/authorization-evaluations` | Return decision, effective permissions, constraints, field capabilities, and optional explanation for one subject/context/resource request. |
| GET | `/principals/{principal_id}/permissions` | List effective permission codes using one explicit evaluated-context filter. |
| GET | `/principals/{principal_id}/menus` | Return effective navigation for one explicit tenant context. |
| GET | `/memberships/{membership_id}/roles` | List roles assigned through all effective membership sources. |
| GET | `/memberships/{membership_id}/authorization-sources` | Explain direct and directory-derived authorization sources. |
| GET | `/tenants/{tenant_id}/requestable-roles` | List roles eligible for access requests. |
| GET, POST | `/access-requests` | Filter by tenant/principal or create a request. |
| PUT | `/access-requests/{request_id}/decision` | Set approval decision. |
| PUT | `/access-requests/{request_id}/withdrawal` | Withdraw/relinquish access. |
| PUT | `/access-requests/{request_id}/grant-revocation` | Revoke an approved temporary grant. |
| GET, POST | `/access-reviews` | Filter by tenant or create a review campaign. |
| GET, PATCH | `/access-reviews/{review_id}` | Read, complete, or cancel a review. |
| PUT | `/access-reviews/{review_id}/items/{item_id}/decision` | Decide one snapshotted grant. |

Recommended relationship decision: add global `relationship_id`. The current
seven-part key is too large for a stable item URI and currently forces DELETE to
accept eight selector parameters.

## `files`

| Method | URI | Definition |
| --- | --- | --- |
| GET, POST | `/principals/{principal_id}/files` | List roots or create/upload in the principal scope. |
| GET, POST | `/tenants/{tenant_id}/files` | List roots or create/upload in tenant scope. |
| GET, POST | `/entities/{entity_id}/files` | List roots or create/upload in entity scope. |
| GET, POST | `/departments/{department_id}/files` | List roots or create/upload in department scope. |
| GET, PATCH, DELETE | `/files/{file_id}` | Read metadata, update, or move to trash. |
| GET | `/files/{file_id}/content` | Download content. |
| GET | `/files/{file_id}/items` | List a directory page with cursor pagination. |
| GET | `/files/{root_file_id}/-/{path}` | Resolve metadata by an authorized root-relative path. |
| POST | `/files/{destination_file_id}/operations` | Execute synchronous copy/move against authorized source IDs. |

The operation body does not repeat `destination_id`. The response describes the
completed mutation; this is not presented as a durable background job.

## `applications`

| Method | URI | Definition |
| --- | --- | --- |
| GET, POST | `/tenants/{tenant_id}/identity-providers` | List or create identity providers. |
| PUT, DELETE | `/identity-providers/{provider_id}` | Replace or delete a provider. |
| GET | `/identity-providers/{provider_id}/authorization` | Start the configured browser protocol. |
| GET, POST | `/identity-providers/{provider_id}/callback` | Protocol callback. |
| GET | `/identity-providers/{provider_id}/identity-continuation` | Complete the bound browser continuation. |
| GET, POST | `/tenants/{tenant_id}/oauth-clients` | List or create OAuth clients. |
| PUT, DELETE | `/oauth-clients/{client_id}` | Replace or delete a client. |
| POST | `/oauth-clients/{client_id}/credentials` | Rotate by creating a new credential. |
| GET, POST | `/tenants/{tenant_id}/saml-service-providers` | List or create service providers. |
| PUT, DELETE | `/saml-service-providers/{sp_id}` | Replace or delete a service provider. |
| POST | `/tenants/{tenant_id}/saml-signing-keys` | Create and activate a signing key. |
| GET, POST | `/tenants/{tenant_id}/scim-directories` | List or create inbound directories. |
| PUT | `/scim-directories/{directory_id}` | Replace a directory. |
| GET, POST | `/scim-directories/{directory_id}/credentials` | List or create credentials. |
| DELETE | `/scim-directories/{directory_id}/credentials/{credential_id}` | Revoke a credential. |
| GET, POST | `/tenants/{tenant_id}/scim-targets` | List or create outbound targets. |
| PUT, DELETE | `/scim-targets/{target_id}` | Replace or delete a target. |
| POST | `/scim-targets/{target_id}/provisioning-jobs` | Create a provisioning job. |
| POST | `/scim-targets/{target_id}/deprovisioning-jobs` | Create a deprovisioning job. |
| PUT | `/scim-targets/{target_id}/retirement` | Create/resume retirement singleton state. |

## `audit`

Tenant audit paths retain `/tenants/{tenant_id}/audit/...`; platform audit paths
retain `/platform/audit/...`. Both use the same suffix contract:

| Method | Suffix | Definition |
| --- | --- | --- |
| GET | `/events` and `/events/{event_id}` | List/read immutable events. |
| GET | `/exports` | Export a verified snapshot representation. |
| GET | `/archive-status` | Read archive execution state. |
| GET, POST | `/archives` | List or queue archive jobs. |
| POST | `/archives/{archive_id}/attempts` | Create a retry attempt. |
| GET | `/archives/{archive_id}/content` | Download verified immutable content. |
| POST | `/archives/{archive_id}/verifications` | Create object verification. |
| GET | `/governance-policy` | Read governance status. |
| PUT | `/retention-policy` | Replace retention policy. |
| POST | `/anchors` | Create an external head anchor. |
| GET | `/integrity-verifications` | Compute/read hash-chain verification. |
| POST | `/root-signatures` | Create a signed root commitment. |

Platform scope currently exposes only the supported subset: archive status,
archives, attempts, content, verifications, and retention policy.

## `platform`

| Method | URI | Definition |
| --- | --- | --- |
| GET | `/platform/administrators` | List platform administrator relationships. |
| PUT, DELETE | `/platform/administrators/{principal_id}` | Grant/replace or revoke platform authorization. |
| GET | `/account-closures` | List/filter principal-owned account-closure singletons for operations staff. |
| PATCH | `/principals/{principal_id}/account-closure` | Authorized processing-state transition on the same singleton defined under `account`. |
| GET | `/principals/{principal_id}/lifecycle` | Read retention and erasure blockers. |
| PUT | `/principals/{principal_id}/lifecycle/retention-policy` | Replace retention/hold state. |
| PUT | `/principals/{principal_id}/lifecycle/anonymization` | Perform irreversible anonymization after prerequisites. |

Global suspension uses the same `/principals/{principal_id}/suspension` resource
listed under `account`; the tag/audience may be `platform` for privileged
operations without creating `/platform/principals` duplicates.

## `oauth`, `saml`, And `scim`

Keep protocol-defined contracts unchanged:

- `/.well-known/openid-configuration`, `/.well-known/jwks.json`;
- `/oauth/authorize`, `/oauth/consent`, `/oauth/consent/request`,
  `/oauth/token`, `/oauth/revoke`, `/oauth/introspect`, `/oauth/userinfo`;
- `/saml/{tenant_id}/metadata`, `/sso`, `/slo`;
- `/scim/v2/Bulk`, `/Groups*`, `/Users*`, `/ResourceTypes*`, `/Schemas*`,
  `/ServiceProviderConfig`.

Their specification-defined casing, parameters, status codes, and media types
are exempt from application-path conventions.

## `todos`

| Method | URI | Definition |
| --- | --- | --- |
| GET | `/entities/{entity_id}/todos` | List JSON; `Accept: text/csv` exports the same filtered collection. |
| POST | `/entities/{entity_id}/todos` | `application/json` creates one; supported import media types import a collection. |
| GET, PATCH, DELETE | `/todos/{todo_id}` | Read, update, or mark one Todo deleted. |
| DELETE | `/todos/{todo_id}/deletion` | Remove the deletion marker. |
| GET | `/todo-import-template/content` | Download the import template representation. |

This removes separate `/export` and `/import` action paths by using content
negotiation on the canonical Todo collection.

## Removed Or Consolidated Families

| Current family | Outcome |
| --- | --- |
| `/me/*` | Only `GET /me` remains; children move to their actual resources. |
| `/tenants/{tenant_id}/me/*` | Removed; target principal and evaluated context are explicit. |
| `/tenants/{tenant_id}/principals*` | Removed after principal/membership split. |
| `/platform/principals*` | Removed; privileged callers use canonical principal resources. |
| scoped `*/file-operations` | One destination-file operation endpoint. |
| `/me/personal-token-permissions` | Removed with no replacement. |
| `/roles/{role_id}/permissions` | Consolidated into permission grants. |
| permission `/condition` child | Condition is part of the permission grant. |
| authorization checks/explanations/constraints | One authorization evaluation resource. |
| Todo `/export` and `/import` | Canonical collection with content negotiation. |
| `/guid*` | Removed from public API. |

## Decisions Required For Approval

1. Add `membership_id` and use top-level canonical `/memberships` resources.
2. Add `relationship_id` and use `/relationships/{relationship_id}` items.
3. Replace the full login family with `/authentication-transactions` in one
   migration, not through piecemeal renames.
4. Split role directory bindings into group and position relationship
   collections, matching the actual models.
5. Use `/principals/{principal_id}/account-closure` as the one canonical
   self/admin singleton and `/account-closures` only as its cross-principal
   administrative collection projection.
6. Allow Todo import/export through media types on the canonical collection.

No runtime route is authorized until these six decisions and this manifest are
explicitly confirmed.
