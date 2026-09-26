import type { components } from "./api-schema"
import {
  apiRoute,
  pagedRoute,
  type ApiRoute,
  type PagedRoute,
  type RouteQuery,
} from "./api-route"

export type GrantedConsent = components["schemas"]["GrantedConsent"]
export type FilePage = components["schemas"]["FilePage"]
export type FileChildrenPage = components["schemas"]["FileChildrenPage"]
export type FileEntryView = components["schemas"]["FileEntryView"]
export type FileTrashResult = components["schemas"]["FileTrashResult"]
export type FileOperationResult = components["schemas"]["OperationResult"]
export type FileOperationPolicy =
  components["schemas"]["FileOperationDecisionInput"]["policy"]
export type FileOperationDecision =
  components["schemas"]["FileOperationDecisionInput"]

export interface FileOperationConflictNode {
  node_id?: string
  created_from_id?: string
  kind: string
  name: string
  revision: number
}

export interface FileOperationConflict {
  key: string
  kind: "file" | "directory" | "type"
  source: FileOperationConflictNode
  target: FileOperationConflictNode
  parent: Pick<FileOperationConflictNode, "node_id" | "created_from_id">
  policies: FileOperationPolicy[]
}

export interface FileOperationConflicts {
  space_revision: number
  conflicts: FileOperationConflict[]
}

export interface FileOperationInput {
  action: "copy" | "move"
  destination_id: string
  sources: string[]
  file_policy?: "ask" | "overwrite" | "skip" | "rename"
  directory_policy?: "ask" | "merge" | "replace" | "skip" | "rename"
  type_policy?: "ask" | "replace" | "skip" | "rename"
  observed_revision?: number
  decisions?: FileOperationDecision[]
}

export interface FieldDefinition {
  resource: string
  name: string
  mutability: "readOnly" | "readWrite" | "immutable" | "writeOnly"
  returned: "always" | "never" | "default" | "request"
  maskable: boolean
  configurable: boolean
}

export interface FieldAccess {
  default: "allow" | "deny"
  allow_roles: string[]
  deny_roles: string[]
}

export interface FieldPolicy {
  read: FieldAccess
  write: FieldAccess
  mask: "omit" | "redact"
}

export interface ManagedFieldPolicy {
  resource: string
  field: string
  version: number
  policy: FieldPolicy
}

export interface FieldConstraint {
  revision: number
  fields: Record<
    string,
    {
      read: "allow" | "omit" | "redact"
      create: boolean
      update: boolean
    }
  >
}

export interface Envelope<T> {
  code: number
  message: string
  data: T
  meta?: {
    page?: { offset: number; limit: number; count: number; total: number }
  }
}

export interface Session {
  subject: string
  issuer: string
  /** Authentication context class: how strongly the subject was verified. */
  acr: number
  /** Authentication methods that produced the session, when the service says. */
  amr?: string[]
  auth_time?: string
  preferred_username?: string
  email?: string
  email_verified?: boolean
  organization_id?: string
}

export interface PersonalTokenRequest {
  name: string
  tenant_id: string
  permissions: string[]
  expires_at: string
}
export interface PersonalToken extends PersonalTokenRequest {
  id: string
  created_at: string
  revoked_at?: string
}
export interface IdentityLink {
  id: string
  provider_id: string
  tenant_id: string
  provider_name: string
  provider_type: string
  external_subject: string
  email: string
  display_name: string
}
export interface IdentityLinkProvider {
  id: string
  tenant_id: string
  name: string
  provider_type: "oidc" | "saml"
}
export type ReauthenticationMethod = "password" | "passkey" | "federation"
export interface ReauthenticationChallenge {
  proof: string
  method: ReauthenticationMethod
  expires_at: string
  options?: { publicKey: Record<string, unknown> }
}
export interface PersonalMembership {
  tenant_id: string
  tenant_name: string
  status: string
}
export interface ClosureRequest {
  principal_id: string
  status:
    "none" | "pending" | "processing" | "canceled" | "rejected" | "completed"
  version: number
  requested_at?: string
  updated_at?: string
}

export interface BrowserSession {
  id: string
  current: boolean
  created_at: string
  last_seen_at: string
  expires_at: string
  absolute_expires_at: string
  ip_address?: string
  user_agent?: string
}

export interface LoginResult {
  status: "authenticated" | "mfa_required" | "mfa_enrollment_required"
  subject?: string
  return_url: string
  challenge_id?: string
  expires_at?: string
  methods?: Array<"totp" | "recovery_code">
}

export interface MfaStatus {
  totp_enabled: boolean
  recovery_codes_remaining: number
}

export interface AuthnCapabilities {
  registration: boolean
  password_recovery: boolean
  passkey: boolean
}

export interface Captcha {
  type: "text" | "slide" | "click"
  captcha_id: string
  image_base64?: string
  master_image?: string
  tile_image?: string
  thumb_image?: string
  width?: number
  height?: number
}

export interface MfaEnrollment {
  secret: string
  provisioning_uri: string
  expires_at: string
}

export interface MfaConfirmation {
  recovery_codes: string[]
}

export interface PasskeyOptions {
  challenge_id: string
  expires_at: string
  options: {
    publicKey: Record<string, unknown>
    mediation?: CredentialMediationRequirement
  }
}

export interface Passkey {
  id: string
  name: string
  sign_count: number
  created_at: string
  last_used_at?: string
}

export interface Principal {
  avatar?: AvatarSettings
  id: string
  login_name?: string
  activation_required: boolean
  email?: string
  email_verified?: boolean
  display_name?: string
  status: "active" | "disabled"
  created_at: string
  updated_at: string
}

export interface AvatarSettings {
  style: "robot" | "pixel" | "monster" | "geometric" | "custom"
  url: string
}

export interface PrincipalList {
  items: Principal[]
  total: number
}

export interface PrincipalLifecycle {
  principal_id: string
  version: number
  retain_until: string | null
  legal_hold: boolean
  reference: string
  anonymized_at: string | null
  updated_at: string | null
}

// 阻塞原因是代码而非句子：控制台负责翻译，运维照着去做具体的一件事。
export type PrincipalLifecycleBlocker =
  | "already_anonymized"
  | "legal_hold"
  | "no_retention_recorded"
  | "retention_not_expired"
  | "not_suspended"

export interface PrincipalLifecycleStatus {
  lifecycle: PrincipalLifecycle
  login_name: string
  suspended: boolean
  anonymized: boolean
  blockers: PrincipalLifecycleBlocker[]
}

export interface ServiceAccount {
  id: string
  login_name: string
  display_name: string
  description?: string
  status: "active" | "disabled"
  expires_at?: string
  version: number
  created_at: string
  updated_at: string
}

export interface ServiceAccountList {
  items: ServiceAccount[]
  total: number
}

export interface ServiceAccountCredential {
  id: string
  service_account_id: string
  name: string
  scopes: string[]
  expires_at?: string
  last_used_at?: string
  revoked_at?: string
  created_at: string
}

export interface ServiceAccountCredentialSecret {
  credential: ServiceAccountCredential
  client_secret: string
}

export interface ServiceAccountInput {
  login_name: string
  display_name?: string
  description?: string
  expires_at?: string
}

export interface ServiceAccountUpdate {
  display_name: string
  description?: string
  status: ServiceAccount["status"]
  expires_at?: string
  version: number
}

export interface Invitation {
  id: string
  tenant_id: string
  email: string
  status: "pending" | "accepted" | "revoked" | "expired"
  department_id?: string
  role_ids: string[]
  expires_at: string
  accepted_by?: string
  accepted_at?: string
  revoked_at?: string
  created_at: string
  updated_at: string
}

export interface IssuedInvitation {
  invitation: Invitation
  token: string
}

export interface InvitationAcceptance {
  invitation_id: string
  tenant_id: string
  principal_id: string
  email: string
  already_accepted: boolean
}

export interface Tenant {
  id: string
  slug: string
  name: string
  status: "active" | "suspended" | "deleted"
  version: number
  created_at: string
  updated_at: string
}

export interface TenantInput {
  slug: string
  name: string
  /** Required: a tenant is created together with the principal who will administer it. */
  owner_principal_id: string
}

export interface TenantUpdate {
  name?: string
  status?: "active" | "suspended"
  version: number
}

export interface Member {
  tenant_id: string
  subject: string
  display_name?: string
  email?: string
  department_id?: string
  status: "active" | "disabled"
  role_ids?: string[]
  disabled_at?: string
  created_at: string
  updated_at: string
}

export type MemberAuthorizationSourceType =
  | "permanent_role"
  | "temporary_role"
  | "static_group"
  | "dynamic_group"
  | "position"
  | "entity_binding"
  | "relationship"

export interface MemberAuthorizationSource {
  type: MemberAuthorizationSourceType
  source_id: string
  source_name?: string
  role_id?: string
  role_name?: string
  permission_codes: string[]
  effect: "allow" | "deny"
  scope_type: string
  scope_id: string
  scope_name?: string
  data_scope?: DataScope
  department_ids: string[]
  expires_at?: string
  inherited: boolean
  relation?: RelationshipRelation
  relationship_path: RelationshipInput[]
}

export interface MemberAuthorizationSnapshot {
  subject: string
  revision: number
  evaluated_at: string
  sources: MemberAuthorizationSource[]
}

export interface Permission {
  resource: string
  verb: string
  code: string
  scope: string
  summary: string
  allowed_relations: Array<"owner" | "editor" | "viewer">
  data_scoped: boolean
  menu: boolean
}

export interface Role {
  id: string
  tenant_id: string
  name: string
  description: string
  created_at: string
  updated_at: string
}

export interface RolePermissionGrant {
  permission_code: string
  condition?: RelationshipCondition
  created_at: string
}

export type AccessRequestStatus =
  "pending" | "approved" | "rejected" | "cancelled" | "revoked" | "expired"

export interface AccessRequest {
  id: string
  tenant_id: string
  requester_id: string
  role_id: string
  role_name: string
  reason: string
  status: AccessRequestStatus
  access_expires_at: string
  request_expires_at: string
  decided_by?: string
  decision_note?: string
  decided_at?: string
  revoked_by?: string
  revoke_reason?: string
  revoked_at?: string
  created_at: string
  updated_at: string
}

export interface RequestableRole {
  id: string
  name: string
  description: string
}

export interface AccessRequestInput {
  role_id: string
  reason: string
  access_expires_at: string
}

export type AccessReviewStatus = "open" | "completed" | "cancelled" | "expired"
export type AccessReviewDecision = "pending" | "keep" | "revoke"

export interface AccessReviewItem {
  id: string
  review_id: string
  principal_id: string
  principal_name: string
  role_id: string
  role_name: string
  grant_type: "permanent" | "temporary"
  grant_id?: string
  grant_created_at: string
  grant_expires_at?: string
  decision: AccessReviewDecision
  decided_by?: string
  decision_note?: string
  decided_at?: string
}

export interface AccessReview {
  id: string
  tenant_id: string
  name: string
  owner_id: string
  status: AccessReviewStatus
  due_at: string
  completed_at?: string
  created_at: string
  updated_at: string
  total: number
  pending: number
  kept: number
  revoked: number
  items: AccessReviewItem[]
}

export interface AccessReviewInput {
  name: string
  due_at: string
}

export interface RoleDirectoryBinding {
  role_id: string
  assignee_type: "group" | "position"
  assignee_id: string
  created_at: string
}

export type DataScope =
  | "all"
  | "self"
  | "department"
  | "department_and_descendants"
  | "selected_departments"

export interface RoleDataScope {
  role_id: string
  scope: DataScope
  department_ids: string[]
  updated_at?: string
}

export interface Entity {
  id: string
  tenant_id: string
  parent_id?: string
  type: string
  name: string
  status: "active" | "disabled"
  metadata: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface EntityInput {
  parent_id?: string
  type: string
  name: string
  status: Entity["status"]
  metadata: Record<string, unknown>
}

export interface EntityRoleBinding {
  entity_id: string
  role_id: string
  principal_id: string
  effect: "allow" | "deny"
  expires_at?: string
  created_at: string
}

export interface EntityRoleBindingInput {
  effect: EntityRoleBinding["effect"]
  expires_at?: string
}

export type RelationshipSubjectType =
  "principal" | "group" | "position" | "entity"
export type RelationshipRelation = "owner" | "editor" | "viewer"

export type RelationshipConditionExpression =
  | { all: RelationshipConditionExpression[] }
  | { any: RelationshipConditionExpression[] }
  | { not: RelationshipConditionExpression }
  | {
      eq: [
        { context: "auth.acr" | "client.id" | "network.zone" },
        { value: number | string },
      ]
    }
  | {
      neq: [
        { context: "auth.acr" | "client.id" | "network.zone" },
        { value: number | string },
      ]
    }
  | { gt: [{ context: "auth.acr" }, { value: number }] }
  | { gte: [{ context: "auth.acr" }, { value: number }] }
  | { lt: [{ context: "auth.acr" }, { value: number }] }
  | { lte: [{ context: "auth.acr" }, { value: number }] }
  | {
      in: [{ context: "client.id" | "network.zone" }, { value: string[] }]
    }
  | { contains: [{ context: "auth.amr" }, { value: string }] }
  | { between_time: [string, string, string] }

export type RelationshipCondition = {
  version: 1
} & RelationshipConditionExpression

export interface RelationshipInput {
  entity_id?: string
  subject_type: RelationshipSubjectType
  subject_id: string
  subject_relation?: "member" | RelationshipRelation
  relation: RelationshipRelation
  resource_type: string
  resource_id: string
  starts_at?: string
  ends_at?: string
  condition?: RelationshipCondition
}

export interface Relationship extends RelationshipInput {
  created_at: string
}

export interface ResourceRef {
  type: string
  id: string
}

export interface DataConstraint {
  allow_all: boolean
  owner_ids: string[]
  resource_ids: string[]
  department_ids: string[]
  ancestors: ResourceRef[]
  denied_ids: string[]
  revision: number
}

export interface AuthorizationDecisionMatch {
  permission_code: string
  role_id: string
  source_type: string
  source_id: string
  scope_type: string
  scope_id: string
  effect: "allow" | "deny"
  inherited: boolean
  relation?: RelationshipRelation
  path?: RelationshipInput[]
}

export interface AuthorizationExplanation {
  allowed: boolean
  reason:
    | "inactive_membership"
    | "inactive_tenant"
    | "inactive_resource"
    | "explicit_deny"
    | "administrator_scope_denied"
    | "permission_grant"
    | "administrator_grant"
    | "relationship_grant"
    | "no_matching_grant"
  revision: number
  matches: AuthorizationDecisionMatch[]
}

export interface Department {
  id: string
  tenant_id: string
  entity_id: string
  parent_id?: string
  name: string
  status: "active" | "disabled"
  sort_order: number
  depth: number
  version: number
  created_at: string
  updated_at: string
}

export interface DepartmentInput {
  entity_id: string
  parent_id?: string
  name: string
  status: "active" | "disabled"
  sort_order: number
}

export interface Position {
  id: string
  tenant_id: string
  code: string
  name: string
  status: "active" | "disabled"
  sort_order: number
  version: number
  created_at: string
  updated_at: string
}

export interface PositionInput {
  code: string
  name: string
  status: "active" | "disabled"
  sort_order: number
}

export interface PositionMember {
  position_id: string
  principal_id: string
  display_name: string
  email?: string
  starts_at?: string
  ends_at?: string
  created_at: string
  updated_at: string
}

export interface MembershipWindowInput {
  starts_at?: string
  ends_at?: string
}

export type PositionMemberInput = MembershipWindowInput

export interface Group {
  id: string
  tenant_id: string
  name: string
  type: "static" | "dynamic"
  membership_rule?: GroupMembershipRule
  description?: string
  status: "active" | "disabled"
  sort_order: number
  version: number
  created_at: string
  updated_at: string
}

export interface GroupInput {
  name: string
  type: Group["type"]
  membership_rule?: GroupMembershipRule
  description: string
  status: "active" | "disabled"
  sort_order: number
}

export type GroupRuleField =
  | "member.subject"
  | "member.email"
  | "member.email_domain"
  | "member.department_id"
  | "member.status"

export interface GroupRuleCondition {
  field: GroupRuleField
  operator: "in" | "not_in"
  values: string[]
}

export interface GroupMembershipRule {
  version: 1
  match: "all" | "any"
  conditions: GroupRuleCondition[]
}

export interface GroupMember {
  group_id: string
  principal_id: string
  display_name: string
  email?: string
  starts_at?: string
  ends_at?: string
  created_at: string
  updated_at: string
}

export type GroupMemberInput = MembershipWindowInput

export interface Menu {
  translations?: Record<string, string>
  id: string
  tenant_id: string
  parent_id?: string
  label: string
  route?: string
  icon?: string
  sort_order: number
  permission_code?: string
  status: "active" | "disabled"
  created_at: string
  updated_at: string
}

export interface MenuInput {
  translations?: Record<string, string>
  parent_id?: string
  label: string
  route?: string
  icon?: string
  sort_order: number
  permission_code?: string
  status: "active" | "disabled"
}

export interface EffectiveMenu {
  translations?: Record<string, string>
  id: string
  label: string
  path?: string
  icon?: string
  sort_order: number
  permission_code: string
  children?: EffectiveMenu[]
}

export type OAuthGrantType =
  "authorization_code" | "refresh_token" | "client_credentials"

export type OAuthConsentMode = "implicit" | "explicit"

export interface OAuthClient {
  id: string
  tenant_id: string
  name: string
  consent_mode: OAuthConsentMode
  redirect_uris: string[]
  grant_types: OAuthGrantType[]
  scopes: string[]
  public_client: boolean
  status: "active" | "disabled"
}

export interface OAuthClientInput {
  name: string
  redirect_uris: string[]
  grant_types: OAuthGrantType[]
  scopes: string[]
  public_client: boolean
}

type OAuthClientResponse = Omit<
  OAuthClient,
  "redirect_uris" | "grant_types" | "scopes"
> & {
  redirect_uris: string[] | null
  grant_types: OAuthGrantType[] | null
  scopes: string[] | null
}

function normalizeOAuthClient(client: OAuthClientResponse): OAuthClient {
  return {
    ...client,
    redirect_uris: client.redirect_uris ?? [],
    grant_types: client.grant_types ?? [],
    scopes: client.scopes ?? [],
  }
}

export interface OAuthClientCreateResult {
  client: OAuthClient
  client_secret?: string
}

export interface SCIMDirectory {
  id: string
  tenant_id: string
  service_account_id?: string
  name: string
  status: "active" | "disabled"
  version: number
  created_at: string
  updated_at: string
}

export interface SCIMDirectoryInput {
  name: string
  service_account_id?: string | null
}

export interface SCIMCredential {
  id: string
  directory_id: string
  name: string
  expires_at?: string
  last_used_at?: string
  revoked_at?: string
  created_at: string
}

export interface SCIMCredentialSecret {
  credential: SCIMCredential
  bearer_token: string
}

export interface SCIMTarget {
  id: string
  tenant_id: string
  name: string
  base_url: string
  status: "active" | "disabled"
  service_account_id?: string
  version: number
  created_at: string
  updated_at: string
}

export interface SCIMTargetInput {
  name: string
  base_url: string
  bearer_token: string
  service_account_id?: string | null
}

export interface SCIMTargetUpdate {
  name: string
  base_url: string
  status: SCIMTarget["status"]
  version: number
  bearer_token?: string
  service_account_id?: string | null
}

export interface SCIMTargetSecret {
  target: SCIMTarget
  bearer_token: string
}

export interface SCIMResourceInput {
  resource_type: "User" | "Group"
  resource_id: string
}

export interface SCIMPushResult extends SCIMResourceInput {
  target_id: string
  external_id: string
  remote_id?: string
}

export interface AuditEvent {
  id: string
  tenant_id: string
  principal_id?: string
  event_type: string
  target_type?: string
  target_id?: string
  outcome: "success" | "denied" | "failure"
  ip_address?: string
  user_agent?: string
  detail: Record<string, unknown>
  sequence: number
  previous_hash?: string
  event_hash: string
  created_at: string
}

export interface AuditIntegrity {
  tenant_id: string
  valid: boolean
  verified_events: number
  head_sequence: number
  head_hash?: string
  anchor?: AuditAnchorStatus
}

export interface AuditExportDownload {
  blob: Blob
  filename: string
}

export interface AuditAnchor {
  schema: string
  tenant_id: string
  head_sequence: number
  head_hash: string
  anchored_at: string
  previous_anchor_hash?: string
  anchor_hash: string
  signing_key_id?: string
  root_public_key?: string
  root_signature?: string
}

export interface AuditAnchorStatus {
  enabled: boolean
  sequence?: number
  hash?: string
  anchored_at?: string
  signed: boolean
  signature_valid?: boolean
  valid: boolean
}

export interface AuditRetentionPolicy {
  tenant_id: string | null
  min_days: number
  archive_after_days: number
  updated_at?: string
}

export interface AuditGovernance {
  policy: AuditRetentionPolicy
  integrity: AuditIntegrity
  anchors: AuditAnchor[]
  total_events: number
  archive_ready_events: number
  anchored_events: number
}

export type AuditScope = "tenant" | "platform"

export interface AuditArchiveObject {
  bucket: string
  key: string
  version_id: string
  sha256: string
  size: number
  retain_until: string
}

export interface AuditArchiveJob {
  id: string
  tenant_id: string | null
  after_sequence: number
  head_sequence: number
  state: "queued" | "running" | "failed" | "complete"
  attempts: number
  next_attempt: number
  error_code?: string
  object?: AuditArchiveObject
  retain_until: string
  created_at: string
  updated_at: string
}

export interface AuditArchiveStatus {
  enabled: boolean
  policy: AuditRetentionPolicy
  total_events: number
  eligible_events: number
  archived_events: number
  pending_events: number
  queued_jobs: number
  running_jobs: number
  failed_jobs: number
  completed_jobs: number
}

const tenantKey = "iam.tenant"
const baseUrl = (import.meta.env.VITE_API_URL ?? "/api").replace(/\/$/, "")
const clientErrorMessages = {
  "en-US": {
    invalid_response: "The service returned an invalid response.",
    clipboard_access_denied:
      "The browser denied clipboard access. Copy the value manually.",
    invalid_audit_export_content_type:
      "The audit export returned an unexpected file type.",
    incomplete_audit_export: "The audit export is incomplete.",
  },
  "zh-CN": {
    invalid_response: "服务返回了无效响应。",
    clipboard_access_denied: "浏览器拒绝访问剪贴板，请手动复制。",
    invalid_audit_export_content_type: "审计导出返回了非预期文件类型。",
    incomplete_audit_export: "审计导出不完整。",
  },
  "ms-MY": {
    invalid_response: "Perkhidmatan mengembalikan respons yang tidak sah.",
    clipboard_access_denied:
      "Pelayar menolak akses papan keratan. Salin nilai secara manual.",
    invalid_audit_export_content_type:
      "Eksport audit mengembalikan jenis fail yang tidak dijangka.",
    incomplete_audit_export: "Eksport audit tidak lengkap.",
  },
}

interface ErrorResponse {
  code?: number | string
  message?: string
  detail?: string
  data?: unknown
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly data: unknown

  constructor(
    status: number,
    code: string,
    message = clientErrorMessage(code),
    data?: unknown
  ) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.code = code
    this.data = data
  }
}

/**
 * The tenant whose data the console is currently showing.
 *
 * A build cannot know which tenants a deployment has, so there is no compiled-in
 * fallback slug: the value is either one the operator picked, one the deployment
 * configured, or one adopted from the session (see `adoptSessionTenant`). Empty
 * means "not resolved yet" and no tenant resource request can be constructed.
 */
export function getTenant(): string {
  const stored =
    typeof localStorage === "undefined" ? null : localStorage.getItem(tenantKey)
  return stored ?? import.meta.env.VITE_DEFAULT_TENANT ?? ""
}

/**
 * Adopt the tenant the service says this session belongs to.
 *
 * Called when a session is established, so that a first login lands on the
 * tenant the account actually belongs to instead of on a guess. Preserve a
 * choice only for the same account; AppLayout validates it against the current
 * accessible tenant directory before loading tenant data.
 */
export function adoptSessionTenant(
  session: Pick<Session, "organization_id"> & Partial<Pick<Session, "subject">>
) {
  if (session.subject && typeof localStorage !== "undefined") {
    const ownerKey = `${tenantKey}:subject`
    if (localStorage.getItem(ownerKey) !== session.subject) {
      setTenant(session.organization_id ?? "")
      localStorage.setItem(ownerKey, session.subject)
      return
    }
  }
  if (!session.organization_id || getTenant()) return
  setTenant(session.organization_id)
}

export function setTenant(value: string): void {
  localStorage.setItem(tenantKey, value.trim())
  window.dispatchEvent(new Event("tenant-change"))
}

export function shouldNotifyMenuCatalogChange(
  path: string,
  method = "GET"
): boolean {
  if (!path.startsWith("/") || ["GET", "HEAD"].includes(method.toUpperCase()))
    return false
  const segments = path.split("?", 1)[0]?.split("/").filter(Boolean) ?? []
  const resourceIndex = ["files", "file-operations"].includes(segments[0] ?? "")
    ? 0
    : segments[0] === "me"
      ? 1
      : segments[0] === "entities"
        ? 2
        : segments[0] === "tenants" && segments[2] === "entities"
          ? 4
          : segments[0] === "tenants"
            ? 2
            : -1
  return !["files", "file-operations"].includes(segments[resourceIndex] ?? "")
}

async function requestWithBase<T>(
  apiBase: string,
  route: ApiRoute,
  init: Omit<RequestInit, "method"> = {}
): Promise<T> {
  return (await requestEnvelopeWithBase<T>(apiBase, route, init)).data
}

async function requestEnvelopeWithBase<T>(
  apiBase: string,
  route: ApiRoute,
  init: Omit<RequestInit, "method"> = {}
): Promise<Envelope<T>> {
  const headers = new Headers(init.headers)
  if (
    typeof document !== "undefined" &&
    document.documentElement.lang &&
    !headers.has("Accept-Language")
  )
    headers.set("Accept-Language", document.documentElement.lang)
  if (
    init.body &&
    (typeof FormData === "undefined" || !(init.body instanceof FormData))
  )
    headers.set("Content-Type", "application/json")
  const response = await fetch(`${apiBase}${route.url}`, {
    ...init,
    method: route.method,
    headers,
    credentials: "include",
  })
  if (
    response.ok &&
    shouldNotifyMenuCatalogChange(route.url, route.method) &&
    typeof window !== "undefined"
  ) {
    window.dispatchEvent(new Event("menu-catalog-change"))
  }
  if (response.status === 204)
    return { code: 0, message: "ok", data: undefined as T }
  const body = (await response.json().catch(() => null)) as
    (Envelope<T> & ErrorResponse) | null
  if (!response.ok) throw apiError(response.status, body)
  if (!body) throw new ApiError(response.status, "invalid_response")
  return body
}

async function requestFileDownloadWithBase(
  apiBase: string,
  route: ApiRoute,
  options: { maxBytes?: number; signal?: AbortSignal } = {}
): Promise<Blob> {
  const response = await fetch(`${apiBase}${route.url}`, {
    method: route.method,
    credentials: "include",
    signal: options.signal,
  })
  if (!response.ok) {
    const body = (await response
      .json()
      .catch(() => null)) as ErrorResponse | null
    throw apiError(response.status, body)
  }
  const declaredLength = response.headers.get("content-length")
  if (
    options.maxBytes !== undefined &&
    declaredLength !== null &&
    /^\d+$/u.test(declaredLength) &&
    Number(declaredLength) > options.maxBytes
  ) {
    await response.body?.cancel()
    throw new ApiError(413, "file_preview_too_large")
  }
  const blob = await response.blob()
  if (options.maxBytes !== undefined && blob.size > options.maxBytes)
    throw new ApiError(413, "file_preview_too_large")
  return blob
}

async function requestAuditDownloadWithBase(
  apiBase: string,
  route: ApiRoute
): Promise<AuditExportDownload> {
  const response = await fetch(`${apiBase}${route.url}`, {
    method: route.method,
    credentials: "include",
  })
  if (!response.ok) {
    const body = (await response
      .json()
      .catch(() => null)) as ErrorResponse | null
    throw apiError(response.status, body)
  }
  if (!response.headers.get("content-type")?.startsWith("application/x-ndjson"))
    throw new ApiError(502, "invalid_audit_export_content_type")
  const blob = await response.blob()
  const tail = await blob.slice(Math.max(0, blob.size - 4096)).text()
  const lastLine = tail.trimEnd().split("\n").at(-1)
  let completion: unknown
  try {
    completion = lastLine ? JSON.parse(lastLine) : null
  } catch {
    completion = null
  }
  if (!isAuditExportCompletion(completion))
    throw new ApiError(502, "incomplete_audit_export")
  return {
    blob,
    filename: auditExportFilename(response.headers.get("content-disposition")),
  }
}

function apiError(status: number, body: ErrorResponse | null): ApiError {
  const message = body?.message?.trim()
  const code =
    (typeof body?.code === "string" ? body.code.trim() : "") ||
    body?.detail?.trim() ||
    message ||
    `HTTP ${status}`
  return new ApiError(status, code, message || undefined, body?.data)
}

export function fileOperationConflicts(
  cause: unknown
): FileOperationConflicts | null {
  if (!(cause instanceof ApiError) || cause.status !== 409) return null
  const data = cause.data
  if (!data || typeof data !== "object") return null
  const candidate = data as Partial<FileOperationConflicts>
  const kinds = new Set(["file", "directory", "type"])
  const policies = new Set(["overwrite", "merge", "replace", "skip", "rename"])
  if (
    !Number.isSafeInteger(candidate.space_revision) ||
    (candidate.space_revision ?? 0) <= 0 ||
    !Array.isArray(candidate.conflicts) ||
    candidate.conflicts.length === 0 ||
    !candidate.conflicts.every(
      (conflict) =>
        conflict &&
        /^[0-9a-f]{64}$/u.test(conflict.key) &&
        kinds.has(conflict.kind) &&
        typeof conflict.source?.name === "string" &&
        typeof conflict.target?.name === "string" &&
        Array.isArray(conflict.policies) &&
        conflict.policies.length > 0 &&
        conflict.policies.every((policy) => policies.has(policy))
    )
  )
    return null
  return candidate as FileOperationConflicts
}

export function clientErrorMessage(code: string): string {
  const language =
    (typeof document !== "undefined" && document.documentElement.lang) ||
    (typeof navigator !== "undefined" && navigator.language) ||
    "en-US"
  const locale = language.toLowerCase().startsWith("zh")
    ? "zh-CN"
    : language.toLowerCase().startsWith("ms")
      ? "ms-MY"
      : "en-US"
  return (
    clientErrorMessages[locale][
      code as keyof (typeof clientErrorMessages)[typeof locale]
    ] ?? code
  )
}

function isAuditExportCompletion(value: unknown): value is {
  type: "complete"
  exported_events: number
  content_sha256: string
} {
  if (!value || typeof value !== "object") return false
  const record = value as Record<string, unknown>
  return (
    record.type === "complete" &&
    typeof record.exported_events === "number" &&
    Number.isSafeInteger(record.exported_events) &&
    record.exported_events >= 0 &&
    typeof record.content_sha256 === "string" &&
    /^[a-f0-9]{64}$/.test(record.content_sha256)
  )
}

function auditExportFilename(disposition: string | null): string {
  const candidate = disposition?.match(/filename="([^"]+)"/i)?.[1]
  return candidate && /^[a-zA-Z0-9._-]+$/.test(candidate)
    ? candidate
    : "audit-export.ndjson"
}

export function createIamApi(
  apiBase = baseUrl,
  tenantID: () => string = getTenant,
  explicitPrincipalID?: () => string
) {
  const request = async <T>(
    route: ApiRoute | Promise<ApiRoute>,
    init: Omit<RequestInit, "method"> = {}
  ) => requestWithBase<T>(apiBase, await route, init)
  let subject: string | undefined
  let discovery: Promise<Session> | undefined
  let identityEpoch = 0
  const resetPrincipal = () => {
    identityEpoch++
    subject = undefined
    discovery = undefined
  }
  const discoverSession = async () => {
    const epoch = identityEpoch
    const session = await request<Session>(apiRoute("get", "/me"))
    if (epoch !== identityEpoch) throw new ApiError(401, "unauthorized")
    subject = session.subject
    return session
  }
  const principalID = async () => {
    const epoch = identityEpoch
    const explicit = explicitPrincipalID?.()
    if (explicit) return explicit
    if (!subject) {
      if (!discovery) {
        const pending = discoverSession().finally(() => {
          if (discovery === pending) discovery = undefined
        })
        discovery = pending
      }
      await discovery
    }
    if (!subject || epoch !== identityEpoch)
      throw new ApiError(401, "unauthorized")
    return subject
  }
  const requestEnvelope = <T>(route: ApiRoute) =>
    requestEnvelopeWithBase<T>(apiBase, route)
  const fileOperationRoute = async (entityID: string | null | undefined) =>
    entityID
      ? apiRoute(
          "post",
          "/tenants/{tenant_id}/entities/{entity_id}/file-operations",
          { path: { tenant_id: tenantID(), entity_id: entityID } }
        )
      : entityID === null
        ? apiRoute("post", "/principals/{principal_id}/file-operations", {
            path: { principal_id: await principalID() },
          })
        : apiRoute("post", "/tenants/{tenant_id}/file-operations", {
            path: { tenant_id: tenantID() },
          })
  const fileCollectionRoute = async (
    entityID: string | null | undefined,
    method: "get" | "post",
    query?: RouteQuery<"/principals/{principal_id}/files">
  ) => {
    if (method === "post")
      return entityID
        ? apiRoute("post", "/entities/{entity_id}/files", {
            path: { entity_id: entityID },
          })
        : entityID === null
          ? apiRoute("post", "/principals/{principal_id}/files", {
              path: { principal_id: await principalID() },
            })
          : apiRoute("post", "/tenants/{tenant_id}/files", {
              path: { tenant_id: tenantID() },
            })
    return entityID
      ? apiRoute("get", "/entities/{entity_id}/files", {
          path: { entity_id: entityID },
          query,
        })
      : entityID === null
        ? apiRoute("get", "/principals/{principal_id}/files", {
            path: { principal_id: await principalID() },
            query,
          })
        : apiRoute("get", "/tenants/{tenant_id}/files", {
            path: { tenant_id: tenantID() },
            query,
          })
  }
  const fileContentURL = (
    _entityID: string | null | undefined,
    fileID: string
  ) =>
    `${apiBase}${apiRoute("get", "/files/{file_id}/content", { path: { file_id: fileID }, query: { disposition: "inline" } }).url}`
  /**
   * Every list endpoint is bounded by default on the server, so a caller that
   * needs the complete set -- a picker's universe, or a membership the UI
   * checks boxes against -- has to walk the pages. A partial answer there is a
   * wrong screen, not a short one: an assigned member would render unassigned.
   * Callers that genuinely want a single page pass offset and limit themselves.
   */
  const requestAllPages = async <T>(route: PagedRoute): Promise<T[]> => {
    const pageSize = 200
    const items: T[] = []
    for (let offset = 0; ; offset += pageSize) {
      const result = await requestEnvelope<T[]>(route.page(offset, pageSize))
      const page = result.data ?? []
      items.push(...page)
      const total = result.meta?.page?.total ?? items.length
      if (page.length === 0 || items.length >= total) break
    }
    return items
  }
  return {
    files: (entityID?: string | null, after = "") =>
      request<FilePage>(
        fileCollectionRoute(entityID, "get", {
          limit: 100,
          after: after || undefined,
        })
      ),
    trashedFiles: (entityID?: string | null, after = "") =>
      request<FilePage>(
        fileCollectionRoute(entityID, "get", {
          limit: 100,
          state: "trashed",
          after: after || undefined,
        })
      ),
    createAccountFileRoot: () => {
      const body = new FormData()
      body.set("kind", "directory")
      body.set("scope", "account")
      return request<FileEntryView>(fileCollectionRoute(null, "post"), { body })
    },
    fileItems: (
      _entityID: string | null | undefined,
      fileID: string,
      after = ""
    ) =>
      request<FileChildrenPage>(
        apiRoute("get", "/files/{file_id}/items", {
          path: { file_id: fileID },
          query: { limit: 100, after: after || undefined },
        })
      ),
    createDirectory: (
      entityID: string | null | undefined,
      parentID: string,
      name: string
    ) => {
      const body = new FormData()
      body.set("parent_id", parentID)
      body.set("kind", "directory")
      body.set("name", name)
      return request<FileEntryView>(fileCollectionRoute(entityID, "post"), {
        body,
      })
    },
    uploadFile: (
      entityID: string | null | undefined,
      parentID: string,
      file: File
    ) => {
      const body = new FormData()
      body.set("parent_id", parentID)
      body.set("kind", "file")
      body.set("file", file)
      return request<FileEntryView>(fileCollectionRoute(entityID, "post"), {
        body,
      })
    },
    fileByPath: (
      _entityID: string | null | undefined,
      rootFileID: string,
      path: string
    ) =>
      request<FileEntryView>(
        apiRoute("get", "/files/{root_file_id}/-/{path}", {
          path: { root_file_id: rootFileID, path },
        })
      ),
    downloadFile: (_entityID: string | null | undefined, fileID: string) =>
      requestFileDownloadWithBase(
        apiBase,
        apiRoute("get", "/files/{file_id}/content", {
          path: { file_id: fileID },
        })
      ),
    fileContentURL,
    previewFile: (
      _entityID: string | null | undefined,
      fileID: string,
      maxBytes: number,
      signal?: AbortSignal
    ) =>
      requestFileDownloadWithBase(
        apiBase,
        apiRoute("get", "/files/{file_id}/content", {
          path: { file_id: fileID },
        }),
        { maxBytes, signal }
      ),
    renameFileEntry: (
      _entityID: string | null | undefined,
      fileID: string,
      revision: number,
      name: string
    ) =>
      request<FileEntryView>(
        apiRoute("patch", "/files/{file_id}", { path: { file_id: fileID } }),
        { body: JSON.stringify({ name, revision }) }
      ),
    restoreFileEntry: (
      _entityID: string | null | undefined,
      fileID: string,
      revision: number,
      name?: string
    ) =>
      request<FileEntryView>(
        apiRoute("patch", "/files/{file_id}", { path: { file_id: fileID } }),
        {
          body: JSON.stringify({
            state: "active",
            revision,
            ...(name ? { name } : {}),
          }),
        }
      ),
    trashFileEntry: (
      _entityID: string | null | undefined,
      fileID: string,
      revision: number
    ) =>
      request<FileTrashResult>(
        apiRoute("delete", "/files/{file_id}", {
          path: { file_id: fileID },
          query: { revision: revision },
        })
      ),
    createFileOperation: (
      entityID: string | null | undefined,
      input: FileOperationInput,
      idempotencyKey: string = crypto.randomUUID()
    ) =>
      request<FileOperationResult>(fileOperationRoute(entityID), {
        headers: {
          "Idempotency-Key": idempotencyKey,
        },
        body: JSON.stringify({
          ...input,
          file_policy: input.file_policy ?? "ask",
          directory_policy: input.directory_policy ?? "ask",
          type_policy: input.type_policy ?? "ask",
        }),
      }),
    capabilities: () =>
      request<AuthnCapabilities>(apiRoute("get", "/login/options")),
    createCaptcha: () =>
      request<Captcha>(apiRoute("post", "/registrations/captchas")),
    register: (body: {
      email: string
      password: string
      display_name?: string
      captcha_type: string
      captcha_id: string
      captcha_answer: string
    }) =>
      request<{ accepted: boolean }>(apiRoute("post", "/registrations"), {
        body: JSON.stringify(body),
      }),
    session: discoverSession,
    sessionForTenant: (tenant: string) => {
      void tenant
      return discoverSession()
    },
    myTenants: async () =>
      request<Tenant[]>(
        apiRoute("get", "/principals/{principal_id}/tenants", {
          path: { principal_id: await principalID() },
        })
      ),
    personalTokenPermissions: async (tenant: string) =>
      request<Permission[]>(
        apiRoute(
          "get",
          "/principals/{principal_id}/personal-token-permissions",
          {
            path: { principal_id: await principalID() },
            query: { tenant_id: tenant },
          }
        )
      ),
    personalTokens: async (offset = 0, limit = 20) => {
      const result = await requestEnvelope<PersonalToken[]>(
        apiRoute("get", "/principals/{principal_id}/personal-tokens", {
          path: { principal_id: await principalID() },
          query: { offset: offset, limit: limit },
        })
      )
      return {
        items: result.data,
        total: result.meta?.page?.total ?? result.data.length,
      }
    },
    preparePersonalToken: async (body: PersonalTokenRequest) =>
      request<{ request: PersonalTokenRequest; target: string }>(
        apiRoute(
          "post",
          "/principals/{principal_id}/personal-token-preparations",
          { path: { principal_id: await principalID() } }
        ),
        { body: JSON.stringify(body) }
      ),
    createPersonalToken: async (
      body: PersonalTokenRequest & { proof: string }
    ) =>
      request<PersonalToken & { token: string }>(
        apiRoute("post", "/principals/{principal_id}/personal-tokens", {
          path: { principal_id: await principalID() },
        }),
        { body: JSON.stringify(body) }
      ),
    revokePersonalToken: async (id: string) =>
      request<{ revoked: boolean }>(
        apiRoute(
          "delete",
          "/principals/{principal_id}/personal-tokens/{token_id}",
          {
            path: { principal_id: await principalID(), token_id: id },
          }
        )
      ),
    reauthenticationMethods: async () =>
      request<{ methods: ReauthenticationMethod[]; mfa_required: boolean }>(
        apiRoute("get", "/principals/{principal_id}/reauthentication/methods", {
          path: { principal_id: await principalID() },
        })
      ),
    createReauthenticationChallenge: async (body: {
      purpose: string
      target: string
      method: ReauthenticationMethod
    }) =>
      request<ReauthenticationChallenge>(
        apiRoute(
          "post",
          "/principals/{principal_id}/reauthentication/challenges",
          { path: { principal_id: await principalID() } }
        ),
        { body: JSON.stringify(body) }
      ),
    verifyReauthentication: async (body: {
      proof: string
      password?: string
      code?: string
      response?: Record<string, unknown>
    }) =>
      request<{ verified: boolean }>(
        apiRoute(
          "post",
          "/principals/{principal_id}/reauthentication/verifications",
          { path: { principal_id: await principalID() } }
        ),
        { body: JSON.stringify(body) }
      ),
    identityLinks: async () =>
      request<IdentityLink[]>(
        apiRoute("get", "/principals/{principal_id}/identity-links", {
          path: { principal_id: await principalID() },
        })
      ),
    identityLinkProviders: async () =>
      request<IdentityLinkProvider[]>(
        apiRoute("get", "/principals/{principal_id}/identity-link-providers", {
          path: { principal_id: await principalID() },
        })
      ),
    createIdentityLinkTransaction: async (body: {
      provider_id: string
      proof: string
      existing_link_id?: string
      return_url: string
    }) =>
      request<{ authorization_url: string }>(
        apiRoute(
          "post",
          "/principals/{principal_id}/identity-link-transactions",
          { path: { principal_id: await principalID() } }
        ),
        { body: JSON.stringify(body) }
      ),
    deleteIdentityLink: async (id: string, proof: string) =>
      request<{ deleted: boolean }>(
        apiRoute(
          "delete",
          "/principals/{principal_id}/identity-links/{link_id}",
          {
            path: { principal_id: await principalID(), link_id: id },
          }
        ),
        { body: JSON.stringify({ proof }) }
      ),
    personalMemberships: async (offset = 0, limit = 50) =>
      request<{ items: PersonalMembership[]; total: number }>(
        apiRoute("get", "/principals/{principal_id}/memberships", {
          path: { principal_id: await principalID() },
          query: { offset: offset, limit: limit },
        })
      ),
    leaveMembership: async (id: string, proof: string) =>
      request<{ status: string }>(
        apiRoute(
          "delete",
          "/principals/{principal_id}/memberships/{tenant_id}",
          {
            path: { principal_id: await principalID(), tenant_id: id },
          }
        ),
        { body: JSON.stringify({ proof }) }
      ),
    closureRequest: async () =>
      request<ClosureRequest>(
        apiRoute("get", "/principals/{principal_id}/closure-request", {
          path: { principal_id: await principalID() },
        })
      ),
    myAvatar: async () =>
      request<AvatarSettings>(
        apiRoute("get", "/principals/{principal_id}/avatar", {
          path: { principal_id: await principalID() },
        })
      ),
    avatarCandidates: async (style?: AvatarSettings["style"]) =>
      request<AvatarSettings[]>(
        apiRoute("post", "/principals/{principal_id}/avatar-candidates", {
          path: { principal_id: await principalID() },
        }),
        {
          body: JSON.stringify({ style }),
        }
      ),
    setMyAvatar: async (avatar: {
      style?: AvatarSettings["style"]
      image_data?: string
      candidate_url?: string
    }) =>
      request<AvatarSettings>(
        apiRoute("put", "/principals/{principal_id}/avatar", {
          path: { principal_id: await principalID() },
        }),
        {
          body: JSON.stringify(avatar),
        }
      ),
    submitClosureRequest: async (proof: string, version: number) =>
      request<ClosureRequest>(
        apiRoute("post", "/principals/{principal_id}/closure-request", {
          path: { principal_id: await principalID() },
        }),
        {
          body: JSON.stringify({ proof, version }),
        }
      ),
    cancelClosureRequest: async (version: number) =>
      request<ClosureRequest>(
        apiRoute("delete", "/principals/{principal_id}/closure-request", {
          path: { principal_id: await principalID() },
        }),
        {
          body: JSON.stringify({ version }),
        }
      ),
    closureRequests: (
      offset = 0,
      limit = 50,
      status: NonNullable<
        RouteQuery<"/platform/closure-requests">["status"]
      > = "pending"
    ) =>
      request<{ items: ClosureRequest[]; total: number }>(
        apiRoute("get", "/platform/closure-requests", {
          query: { status, offset: offset, limit: limit },
        })
      ),
    rejectClosureRequest: (id: string, version: number) =>
      request<ClosureRequest>(
        apiRoute(
          "patch",
          "/platform/principals/{principal_id}/closure-request",
          { path: { principal_id: id } }
        ),
        { body: JSON.stringify({ status: "rejected", version }) }
      ),
    processClosureRequest: (id: string, version: number) =>
      request<ClosureRequest>(
        apiRoute(
          "patch",
          "/platform/principals/{principal_id}/closure-request",
          { path: { principal_id: id } }
        ),
        { body: JSON.stringify({ status: "processing", version }) }
      ),
    oauthConsents: async () =>
      request<GrantedConsent[]>(
        apiRoute("get", "/principals/{principal_id}/oauth-consents", {
          path: { principal_id: await principalID() },
        })
      ),
    revokeOAuthConsent: async (clientID: string) =>
      request<{ revoked: boolean }>(
        apiRoute(
          "delete",
          "/principals/{principal_id}/oauth-consents/{client_id}",
          {
            path: { principal_id: await principalID(), client_id: clientID },
          }
        )
      ),
    login: async (body: {
      login_name: string
      password: string
      return_url: string
    }) => {
      resetPrincipal()
      const epoch = identityEpoch
      const result = await request<LoginResult>(apiRoute("post", "/login"), {
        body: JSON.stringify(body),
      })
      if (epoch === identityEpoch) subject = result.subject
      return result
    },
    verifyLoginMfa: async (body: { challenge_id: string; code: string }) => {
      const result = await request<LoginResult>(
        apiRoute("post", "/login/mfa"),
        {
          body: JSON.stringify(body),
        }
      )
      resetPrincipal()
      subject = result.subject
      return result
    },
    logout: async () => {
      const result = await request<{ logout_url: string }>(
        apiRoute("delete", "/sessions/current")
      )
      resetPrincipal()
      return result
    },
    currentBrowserSession: () =>
      request<BrowserSession>(apiRoute("get", "/sessions/current")),
    browserSession: (id: string) =>
      request<BrowserSession>(
        apiRoute("get", "/sessions/{session_id}", {
          path: { session_id: id },
        })
      ),
    sessions: async () =>
      request<BrowserSession[]>(
        apiRoute("get", "/principals/{principal_id}/sessions", {
          path: { principal_id: await principalID() },
        })
      ),
    revokeSession: (id: string) =>
      request<{ revoked: boolean }>(
        apiRoute("delete", "/sessions/{session_id}", {
          path: { session_id: id },
        })
      ),
    logoutAll: async () => {
      const result = await request<{ logout_url: string }>(
        apiRoute("delete", "/principals/{principal_id}/sessions", {
          path: { principal_id: await principalID() },
        })
      )
      resetPrincipal()
      return result
    },
    changePassword: async (body: {
      current_password: string
      new_password: string
    }) =>
      request<{ changed: boolean }>(
        apiRoute("patch", "/principals/{principal_id}/password", {
          path: { principal_id: await principalID() },
        }),
        {
          body: JSON.stringify(body),
        }
      ),
    startPasswordRecovery: (identifier: string) =>
      request<{ accepted: boolean }>(
        apiRoute("post", "/password-recoveries/requests"),
        { body: JSON.stringify({ identifier }) }
      ),
    completePasswordRecovery: (body: { token: string; new_password: string }) =>
      request<{ changed: boolean }>(apiRoute("post", "/password-recoveries"), {
        body: JSON.stringify(body),
      }),
    startEmailVerification: () =>
      request<{ accepted: boolean }>(
        apiRoute("post", "/email-verification-requests")
      ),
    completeEmailVerification: (token: string) =>
      request<{ verified: boolean }>(apiRoute("post", "/email-verifications"), {
        body: JSON.stringify({ token }),
      }),
    mfaStatus: async () =>
      request<MfaStatus>(
        apiRoute("get", "/principals/{principal_id}/mfa", {
          path: { principal_id: await principalID() },
        })
      ),
    beginTotpEnrollment: async (currentPassword: string) =>
      request<MfaEnrollment>(
        apiRoute("post", "/principals/{principal_id}/mfa/totp-enrollments", {
          path: { principal_id: await principalID() },
        }),
        {
          body: JSON.stringify({ current_password: currentPassword }),
        }
      ),
    confirmTotpEnrollment: async (code: string) =>
      request<MfaConfirmation>(
        apiRoute("post", "/principals/{principal_id}/mfa/totp", {
          path: { principal_id: await principalID() },
        }),
        {
          body: JSON.stringify({ code }),
        }
      ),
    regenerateRecoveryCodes: async (body: {
      current_password: string
      code: string
    }) =>
      request<MfaConfirmation>(
        apiRoute("put", "/principals/{principal_id}/mfa/recovery-codes", {
          path: { principal_id: await principalID() },
        }),
        {
          body: JSON.stringify(body),
        }
      ),
    disableTotp: async (body: { current_password: string; code: string }) =>
      request<{ disabled: boolean }>(
        apiRoute("delete", "/principals/{principal_id}/mfa/totp", {
          path: { principal_id: await principalID() },
        }),
        {
          body: JSON.stringify(body),
        }
      ),
    passkeys: async () =>
      request<Passkey[]>(
        apiRoute("get", "/principals/{principal_id}/passkeys", {
          path: { principal_id: await principalID() },
        })
      ),
    beginPasskeyRegistration: async (currentPassword: string) =>
      request<PasskeyOptions>(
        apiRoute(
          "post",
          "/principals/{principal_id}/passkey-registration-challenges",
          { path: { principal_id: await principalID() } }
        ),
        { body: JSON.stringify({ current_password: currentPassword }) }
      ),
    finishPasskeyRegistration: async (body: {
      challenge_id: string
      name: string
      credential: Record<string, unknown>
    }) =>
      request<Passkey>(
        apiRoute("post", "/principals/{principal_id}/passkeys", {
          path: { principal_id: await principalID() },
        }),
        {
          body: JSON.stringify(body),
        }
      ),
    beginPasskeyLogin: (returnUrl: string) =>
      request<PasskeyOptions>(apiRoute("post", "/login/passkey/options"), {
        body: JSON.stringify({ return_url: returnUrl }),
      }),
    finishPasskeyLogin: async (body: {
      challenge_id: string
      credential: Record<string, unknown>
    }) => {
      resetPrincipal()
      return request<LoginResult>(apiRoute("post", "/login/passkey/verify"), {
        body: JSON.stringify(body),
      })
    },
    renamePasskey: async (id: string, name: string) =>
      request<Passkey>(
        apiRoute("patch", "/principals/{principal_id}/passkeys/{passkey_id}", {
          path: { principal_id: await principalID(), passkey_id: id },
        }),
        { body: JSON.stringify({ name }) }
      ),
    deletePasskey: async (id: string, currentPassword: string) =>
      request<{ deleted: boolean }>(
        apiRoute("delete", "/principals/{principal_id}/passkeys/{passkey_id}", {
          path: { principal_id: await principalID(), passkey_id: id },
        }),
        { body: JSON.stringify({ current_password: currentPassword }) }
      ),
    principals: (search = "") =>
      request<PrincipalList>(
        apiRoute("get", "/tenants/{tenant_id}/principals", {
          path: { tenant_id: tenantID() },
          query: { limit: 200, search: search },
        })
      ),
    principal: (id: string) =>
      request<Principal>(
        apiRoute("get", "/tenants/{tenant_id}/principals/{principal_id}", {
          path: { tenant_id: tenantID(), principal_id: id },
        })
      ),
    createPrincipal: (body: {
      login_name: string
      password: string
      display_name?: string
      email?: string
    }) =>
      request<Principal>(
        apiRoute("post", "/tenants/{tenant_id}/principals", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    updatePrincipal: (
      id: string,
      body: { display_name?: string; email?: string }
    ) =>
      request<Principal>(
        apiRoute("patch", "/tenants/{tenant_id}/principals/{principal_id}", {
          path: { tenant_id: tenantID(), principal_id: id },
        }),
        { body: JSON.stringify(body) }
      ),
    // 后端把停用/恢复重构成同一个 suspension 子资源的 PUT/DELETE。
    // 函数名保持不变，调用方无需改动；响应体仍是 Principal。
    disablePrincipal: (id: string) =>
      request<Principal>(
        apiRoute(
          "put",
          "/tenants/{tenant_id}/principals/{principal_id}/suspension",
          { path: { tenant_id: tenantID(), principal_id: id } }
        )
      ),
    restorePrincipal: (id: string) =>
      request<Principal>(
        apiRoute(
          "delete",
          "/tenants/{tenant_id}/principals/{principal_id}/suspension",
          { path: { tenant_id: tenantID(), principal_id: id } }
        )
      ),
    // 生命周期是平台级操作，租户权限不可达；后端以 platform 守卫强制。
    principalLifecycle: (id: string) =>
      request<PrincipalLifecycleStatus>(
        apiRoute("get", "/platform/principals/{principal_id}/lifecycle", {
          path: { principal_id: id },
        })
      ),
    setPrincipalRetention: (
      id: string,
      body: {
        version: number
        retain_until: string
        legal_hold: boolean
        reference: string
      }
    ) =>
      request<PrincipalLifecycle>(
        apiRoute(
          "put",
          "/platform/principals/{principal_id}/lifecycle/retention-policy",
          { path: { principal_id: id } }
        ),
        { body: JSON.stringify(body) }
      ),
    anonymizePrincipal: (
      id: string,
      body: { version: number; confirmation: string }
    ) =>
      request<PrincipalLifecycle>(
        apiRoute(
          "put",
          "/platform/principals/{principal_id}/lifecycle/anonymization",
          { path: { principal_id: id } }
        ),
        { body: JSON.stringify(body) }
      ),
    serviceAccounts: (search = "") =>
      request<ServiceAccountList>(
        apiRoute("get", "/tenants/{tenant_id}/service-accounts", {
          path: { tenant_id: tenantID() },
          query: { limit: 200, search: search },
        })
      ),
    createServiceAccount: (body: ServiceAccountInput) =>
      request<ServiceAccount>(
        apiRoute("post", "/tenants/{tenant_id}/service-accounts", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    updateServiceAccount: (id: string, body: ServiceAccountUpdate) =>
      request<ServiceAccount>(
        apiRoute("put", "/service-accounts/{service_account_id}", {
          path: { service_account_id: id },
        }),
        { body: JSON.stringify(body) }
      ),
    deleteServiceAccount: (id: string, version: number) =>
      request<{ deleted: boolean }>(
        apiRoute("delete", "/service-accounts/{service_account_id}", {
          path: { service_account_id: id },
          query: { version: version },
        })
      ),
    serviceAccountCredentials: (id: string) =>
      request<ServiceAccountCredential[]>(
        apiRoute("get", "/service-accounts/{service_account_id}/credentials", {
          path: { service_account_id: id },
        })
      ),
    createServiceAccountCredential: (
      id: string,
      body: { name: string; scopes: string[]; expires_at?: string }
    ) =>
      request<ServiceAccountCredentialSecret>(
        apiRoute("post", "/service-accounts/{service_account_id}/credentials", {
          path: { service_account_id: id },
        }),
        { body: JSON.stringify(body) }
      ),
    revokeServiceAccountCredential: (id: string, credentialID: string) =>
      request<{ revoked: boolean }>(
        apiRoute(
          "delete",
          "/service-accounts/{service_account_id}/credentials/{credential_id}",
          { path: { service_account_id: id, credential_id: credentialID } }
        )
      ),
    invitations: () =>
      requestAllPages<Invitation>(
        pagedRoute("/tenants/{tenant_id}/invitations", {
          path: { tenant_id: tenantID() },
        })
      ),
    createInvitation: (body: {
      email: string
      department_id?: string
      role_ids?: string[]
      expires_in_hours?: number
    }) =>
      request<IssuedInvitation>(
        apiRoute("post", "/tenants/{tenant_id}/invitations", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    revokeInvitation: (id: string) =>
      request<{ revoked: boolean }>(
        apiRoute("delete", "/invitations/{invitation_id}", {
          path: { invitation_id: id },
        })
      ),
    resendInvitation: (id: string, expiresInHours = 72) =>
      request<IssuedInvitation>(
        apiRoute("post", "/invitations/{invitation_id}/deliveries", {
          path: { invitation_id: id },
        }),
        { body: JSON.stringify({ expires_in_hours: expiresInHours }) }
      ),
    acceptInvitation: (body: {
      token: string
      login_name: string
      password: string
      display_name?: string
    }) =>
      request<InvitationAcceptance>(
        apiRoute("post", "/invitation-acceptances"),
        { body: JSON.stringify(body) }
      ),
    tenants: (includeDeleted = false) =>
      requestAllPages<Tenant>(
        pagedRoute("/tenants", { query: { include_deleted: includeDeleted } })
      ),
    createTenant: (body: TenantInput) =>
      request<Tenant>(apiRoute("post", "/tenants"), {
        body: JSON.stringify(body),
      }),
    updateTenant: (id: string, body: TenantUpdate) =>
      request<Tenant>(
        apiRoute("patch", "/tenants/{tenant_id}", { path: { tenant_id: id } }),
        { body: JSON.stringify(body) }
      ),
    deleteTenant: (id: string, version: number) =>
      request<{ deleted: boolean }>(
        apiRoute("delete", "/tenants/{tenant_id}", {
          path: { tenant_id: id },
          query: { version: version },
        })
      ),
    members: (search = "") =>
      requestAllPages<Member>(
        pagedRoute("/tenants/{tenant_id}/members", {
          path: { tenant_id: tenantID() },
          query: { search: search },
        })
      ),
    updateMember: (
      subject: string,
      body: Partial<
        Pick<Member, "display_name" | "email" | "department_id" | "status">
      >
    ) =>
      request<Member>(
        apiRoute("patch", "/tenants/{tenant_id}/members/{subject}", {
          path: { tenant_id: tenantID(), subject: subject },
        }),
        { body: JSON.stringify(body) }
      ),
    memberRoles: (subject: string) =>
      request<string[]>(
        apiRoute("get", "/tenants/{tenant_id}/members/{subject}/roles", {
          path: { tenant_id: tenantID(), subject: subject },
        })
      ),
    memberAuthorizationSources: (subject: string) =>
      request<MemberAuthorizationSnapshot>(
        apiRoute(
          "get",
          "/tenants/{tenant_id}/members/{subject}/authorization-sources",
          { path: { tenant_id: tenantID(), subject: subject } }
        )
      ),
    entities: () =>
      request<Entity[]>(
        apiRoute("get", "/tenants/{tenant_id}/entities", {
          path: { tenant_id: tenantID() },
        })
      ),
    entity: (id: string) =>
      request<Entity>(
        apiRoute("get", "/entities/{entity_id}", { path: { entity_id: id } })
      ),
    createEntity: (body: EntityInput) =>
      request<Entity>(
        apiRoute("post", "/tenants/{tenant_id}/entities", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    updateEntity: (id: string, body: Partial<EntityInput>) =>
      request<Entity>(
        apiRoute("patch", "/entities/{entity_id}", { path: { entity_id: id } }),
        { body: JSON.stringify(body) }
      ),
    deleteEntity: (id: string) =>
      request<{ changed: boolean; sync_status: string }>(
        apiRoute("delete", "/entities/{entity_id}", { path: { entity_id: id } })
      ),
    entityRoleBindings: (id: string) =>
      requestAllPages<EntityRoleBinding>(
        pagedRoute("/entities/{entity_id}/role-bindings", {
          path: { entity_id: id },
        })
      ),
    putEntityRoleBinding: (
      id: string,
      roleID: string,
      principalID: string,
      body: EntityRoleBindingInput
    ) =>
      request<EntityRoleBinding>(
        apiRoute(
          "put",
          "/entities/{entity_id}/role-bindings/{role_id}/{principal_id}",
          {
            path: { entity_id: id, role_id: roleID, principal_id: principalID },
          }
        ),
        { body: JSON.stringify(body) }
      ),
    deleteEntityRoleBinding: (
      id: string,
      roleID: string,
      principalID: string
    ) =>
      request<{ changed: boolean; sync_status: string }>(
        apiRoute(
          "delete",
          "/entities/{entity_id}/role-bindings/{role_id}/{principal_id}",
          {
            path: { entity_id: id, role_id: roleID, principal_id: principalID },
          }
        )
      ),
    relationships: (resourceID = "") =>
      requestAllPages<Relationship>(
        pagedRoute("/tenants/{tenant_id}/relationships", {
          path: { tenant_id: tenantID() },
          query: { resource_id: resourceID || undefined },
        })
      ),
    resourceRelationships: (entityID: string) =>
      request<Relationship[]>(
        apiRoute("get", "/tenants/{tenant_id}/relationships", {
          path: { tenant_id: tenantID() },
          query: { entity_id: entityID },
        })
      ),
    putRelationship: (body: RelationshipInput) =>
      request<Relationship>(
        apiRoute("post", "/tenants/{tenant_id}/relationships", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    deleteRelationship: (relationship: RelationshipInput) =>
      request<{ changed: boolean; sync_status: string }>(
        apiRoute("delete", "/tenants/{tenant_id}/relationships", {
          path: { tenant_id: tenantID() },
          query: {
            entity_id: relationship.entity_id || undefined,
            subject_type: relationship.subject_type,
            subject_id: relationship.subject_id,
            subject_relation: relationship.subject_relation || undefined,
            relation: relationship.relation,
            resource_type: relationship.resource_type,
            resource_id: relationship.resource_id,
          },
        })
      ),
    checkEntityAuthorization: (
      entityID: string,
      permissionCode: string,
      subject: string
    ) =>
      request<
        Pick<AuthorizationExplanation, "allowed" | "reason" | "revision">
      >(
        apiRoute("post", "/tenants/{tenant_id}/authorization-checks", {
          path: { tenant_id: tenantID() },
        }),
        {
          body: JSON.stringify({
            entity_id: entityID,
            permission_code: permissionCode,
            subject,
          }),
        }
      ),
    checkResourceAuthorization: (
      entityID: string,
      resourceType: string,
      resourceID: string,
      permissionCode: string,
      subject: string
    ) =>
      request<
        Pick<AuthorizationExplanation, "allowed" | "reason" | "revision">
      >(
        apiRoute("post", "/tenants/{tenant_id}/authorization-checks", {
          path: { tenant_id: tenantID() },
        }),
        {
          body: JSON.stringify({
            entity_id: entityID,
            resource_type: resourceType,
            resource_id: resourceID,
            permission_code: permissionCode,
            subject,
          }),
        }
      ),
    authorizationConstraint: (permissionCode: string, subject: string) =>
      request<DataConstraint>(
        apiRoute(
          "post",
          "/tenants/{tenant_id}/authorization-constraint-evaluations",
          { path: { tenant_id: tenantID() } }
        ),
        {
          body: JSON.stringify({
            permission_code: permissionCode,
            subject,
          }),
        }
      ),
    explainEntityAuthorization: (
      entityID: string,
      permissionCode: string,
      subject: string
    ) =>
      request<AuthorizationExplanation>(
        apiRoute("post", "/tenants/{tenant_id}/authorization-explanations", {
          path: { tenant_id: tenantID() },
        }),
        {
          body: JSON.stringify({
            entity_id: entityID,
            permission_code: permissionCode,
            subject,
          }),
        }
      ),
    explainResourceAuthorization: (
      entityID: string,
      resourceType: string,
      resourceID: string,
      permissionCode: string,
      subject: string
    ) =>
      request<AuthorizationExplanation>(
        apiRoute("post", "/tenants/{tenant_id}/authorization-explanations", {
          path: { tenant_id: tenantID() },
        }),
        {
          body: JSON.stringify({
            entity_id: entityID,
            resource_type: resourceType,
            resource_id: resourceID,
            permission_code: permissionCode,
            subject,
          }),
        }
      ),
    departments: () =>
      requestAllPages<Department>(
        pagedRoute("/tenants/{tenant_id}/departments", {
          path: { tenant_id: tenantID() },
        })
      ),
    department: (id: string) =>
      request<Department>(
        apiRoute("get", "/departments/{department_id}", {
          path: { department_id: id },
        })
      ),
    createDepartment: (body: DepartmentInput) =>
      request<Department>(
        apiRoute("post", "/tenants/{tenant_id}/departments", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    updateDepartment: (
      id: string,
      body: Partial<Omit<DepartmentInput, "entity_id">> &
        Pick<Department, "version">
    ) =>
      request<Department>(
        apiRoute("patch", "/departments/{department_id}", {
          path: { department_id: id },
        }),
        { body: JSON.stringify(body) }
      ),
    deleteDepartment: (id: string, version: number) =>
      request<{ deleted: boolean }>(
        apiRoute("delete", "/departments/{department_id}", {
          path: { department_id: id },
          query: { version: version },
        })
      ),
    positions: () =>
      requestAllPages<Position>(
        pagedRoute("/tenants/{tenant_id}/positions", {
          path: { tenant_id: tenantID() },
        })
      ),
    position: (id: string) =>
      request<Position>(
        apiRoute("get", "/positions/{position_id}", {
          path: { position_id: id },
        })
      ),
    createPosition: (body: PositionInput) =>
      request<Position>(
        apiRoute("post", "/tenants/{tenant_id}/positions", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    updatePosition: (
      id: string,
      body: Partial<PositionInput> & Pick<Position, "version">
    ) =>
      request<Position>(
        apiRoute("patch", "/positions/{position_id}", {
          path: { position_id: id },
        }),
        { body: JSON.stringify(body) }
      ),
    deletePosition: (id: string, version: number) =>
      request<{ deleted: boolean }>(
        apiRoute("delete", "/positions/{position_id}", {
          path: { position_id: id },
          query: { version: version },
        })
      ),
    positionMembers: (id: string) =>
      requestAllPages<PositionMember>(
        pagedRoute("/positions/{position_id}/members", {
          path: { position_id: id },
        })
      ),
    putPositionMember: (
      id: string,
      principalID: string,
      body: PositionMemberInput
    ) =>
      request<PositionMember>(
        apiRoute("put", "/positions/{position_id}/members/{principal_id}", {
          path: { position_id: id, principal_id: principalID },
        }),
        { body: JSON.stringify(body) }
      ),
    deletePositionMember: (id: string, principalID: string) =>
      request<{ deleted: boolean }>(
        apiRoute("delete", "/positions/{position_id}/members/{principal_id}", {
          path: { position_id: id, principal_id: principalID },
        })
      ),
    groups: () =>
      requestAllPages<Group>(
        pagedRoute("/tenants/{tenant_id}/groups", {
          path: { tenant_id: tenantID() },
        })
      ),
    group: (id: string) =>
      request<Group>(
        apiRoute("get", "/groups/{group_id}", { path: { group_id: id } })
      ),
    createGroup: (body: GroupInput) =>
      request<Group>(
        apiRoute("post", "/tenants/{tenant_id}/groups", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    updateGroup: (
      id: string,
      body: Partial<Omit<GroupInput, "type">> & Pick<Group, "version">
    ) =>
      request<Group>(
        apiRoute("patch", "/groups/{group_id}", { path: { group_id: id } }),
        { body: JSON.stringify(body) }
      ),
    deleteGroup: (id: string, version: number) =>
      request<{ deleted: boolean }>(
        apiRoute("delete", "/groups/{group_id}", {
          path: { group_id: id },
          query: { version: version },
        })
      ),
    groupMembers: (id: string) =>
      requestAllPages<GroupMember>(
        pagedRoute("/groups/{group_id}/members", { path: { group_id: id } })
      ),
    putGroupMember: (id: string, principalID: string, body: GroupMemberInput) =>
      request<GroupMember>(
        apiRoute("put", "/groups/{group_id}/members/{principal_id}", {
          path: { group_id: id, principal_id: principalID },
        }),
        { body: JSON.stringify(body) }
      ),
    deleteGroupMember: (id: string, principalID: string) =>
      request<{ deleted: boolean }>(
        apiRoute("delete", "/groups/{group_id}/members/{principal_id}", {
          path: { group_id: id, principal_id: principalID },
        })
      ),
    fieldCatalog: () =>
      request<FieldDefinition[]>(
        apiRoute("get", "/tenants/{tenant_id}/field-catalog", {
          path: { tenant_id: tenantID() },
        })
      ),
    fieldPolicies: (resource = "") =>
      requestAllPages<ManagedFieldPolicy>(
        pagedRoute("/tenants/{tenant_id}/field-policies", {
          path: { tenant_id: tenantID() },
          query: { resource: resource || undefined },
        })
      ),
    setFieldPolicy: (
      resource: string,
      field: string,
      policy: FieldPolicy,
      version: number
    ) =>
      request<ManagedFieldPolicy>(
        apiRoute(
          "put",
          "/tenants/{tenant_id}/field-policies/{resource}/{field}",
          { path: { tenant_id: tenantID(), resource: resource, field: field } }
        ),
        { body: JSON.stringify({ policy, version }) }
      ),
    deleteFieldPolicy: (resource: string, field: string, version: number) =>
      request<{ changed: boolean }>(
        apiRoute(
          "delete",
          "/tenants/{tenant_id}/field-policies/{resource}/{field}",
          {
            path: { tenant_id: tenantID(), resource: resource, field: field },
            query: { version: version },
          }
        )
      ),
    effectiveFields: (resource: string, entityID = "") =>
      request<FieldConstraint>(
        apiRoute("get", "/tenants/{tenant_id}/effective-fields", {
          path: { tenant_id: tenantID() },
          query: { resource, entity_id: entityID || undefined },
        })
      ),
    roles: () =>
      request<Role[]>(
        apiRoute("get", "/tenants/{tenant_id}/roles", {
          path: { tenant_id: tenantID() },
        })
      ),
    requestableRoles: () =>
      requestAllPages<RequestableRole>(
        pagedRoute("/tenants/{tenant_id}/requestable-roles", {
          path: { tenant_id: tenantID() },
        })
      ),
    myAccessRequests: async () =>
      request<AccessRequest[]>(
        apiRoute(
          "get",
          "/tenants/{tenant_id}/principals/{principal_id}/access-requests",
          {
            path: { principal_id: await principalID(), tenant_id: tenantID() },
          }
        )
      ),
    accessRequests: () =>
      requestAllPages<AccessRequest>(
        pagedRoute("/tenants/{tenant_id}/access-requests", {
          path: { tenant_id: tenantID() },
        })
      ),
    createAccessRequest: (body: AccessRequestInput) =>
      request<AccessRequest>(
        apiRoute("post", "/tenants/{tenant_id}/access-requests", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    approveAccessRequest: (id: string, note = "") =>
      request<AccessRequest>(
        apiRoute("put", "/access-requests/{request_id}/decision", {
          path: { request_id: id },
        }),
        { body: JSON.stringify({ decision: "approved", note }) }
      ),
    rejectAccessRequest: (id: string, note = "") =>
      request<AccessRequest>(
        apiRoute("put", "/access-requests/{request_id}/decision", {
          path: { request_id: id },
        }),
        { body: JSON.stringify({ decision: "rejected", note }) }
      ),
    revokeAccessRequest: (id: string, reason = "") =>
      request<AccessRequest>(
        apiRoute("put", "/access-requests/{request_id}/grant-revocation", {
          path: { request_id: id },
        }),
        { body: JSON.stringify({ reason }) }
      ),
    withdrawAccessRequest: (id: string, reason = "") =>
      request<AccessRequest>(
        apiRoute("put", "/access-requests/{request_id}/withdrawal", {
          path: { request_id: id },
        }),
        { body: JSON.stringify({ reason }) }
      ),
    accessReviews: () =>
      requestAllPages<AccessReview>(
        pagedRoute("/tenants/{tenant_id}/access-reviews", {
          path: { tenant_id: tenantID() },
        })
      ),
    accessReview: (id: string) =>
      request<AccessReview>(
        apiRoute("get", "/access-reviews/{review_id}", {
          path: { review_id: id },
        })
      ),
    createAccessReview: (body: AccessReviewInput) =>
      request<AccessReview>(
        apiRoute("post", "/tenants/{tenant_id}/access-reviews", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    decideAccessReviewItem: (
      reviewID: string,
      itemID: string,
      decision: AccessReviewDecision,
      note = ""
    ) =>
      request<AccessReviewItem>(
        apiRoute(
          "put",
          "/access-reviews/{review_id}/items/{item_id}/decision",
          { path: { review_id: reviewID, item_id: itemID } }
        ),
        { body: JSON.stringify({ decision, note }) }
      ),
    // Completing and cancelling are the review's own status, so they are
    // written on the review rather than posted to a verb beside it. The
    // spelling of "cancelled" is the stored state's, not this file's.
    completeAccessReview: (id: string) =>
      request<AccessReview>(
        apiRoute("patch", "/access-reviews/{review_id}", {
          path: { review_id: id },
        }),
        { body: JSON.stringify({ status: "completed" }) }
      ),
    cancelAccessReview: (id: string) =>
      request<AccessReview>(
        apiRoute("patch", "/access-reviews/{review_id}", {
          path: { review_id: id },
        }),
        { body: JSON.stringify({ status: "cancelled" }) }
      ),
    createRole: (body: Pick<Role, "name" | "description">) =>
      request<Role>(
        apiRoute("post", "/tenants/{tenant_id}/roles", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    updateRole: (
      id: string,
      body: Partial<Pick<Role, "name" | "description">>
    ) =>
      request<Role>(
        apiRoute("patch", "/roles/{role_id}", { path: { role_id: id } }),
        { body: JSON.stringify(body) }
      ),
    deleteRole: (id: string) =>
      request(
        apiRoute("delete", "/roles/{role_id}", { path: { role_id: id } })
      ),
    rolePermissions: (id: string) =>
      request<string[]>(
        apiRoute("get", "/roles/{role_id}/permissions", {
          path: { role_id: id },
        })
      ),
    rolePermissionGrants: (id: string) =>
      request<RolePermissionGrant[]>(
        apiRoute("get", "/roles/{role_id}/permission-grants", {
          path: { role_id: id },
        })
      ),
    grantPermission: (id: string, code: string) =>
      request(
        apiRoute("put", "/roles/{role_id}/permissions/{permission_code}", {
          path: { role_id: id, permission_code: code },
        })
      ),
    revokePermission: (id: string, code: string) =>
      request(
        apiRoute("delete", "/roles/{role_id}/permissions/{permission_code}", {
          path: { role_id: id, permission_code: code },
        })
      ),
    setRolePermissionCondition: (
      id: string,
      code: string,
      condition: RelationshipCondition
    ) =>
      request<RolePermissionGrant>(
        apiRoute(
          "put",
          "/roles/{role_id}/permissions/{permission_code}/condition",
          { path: { role_id: id, permission_code: code } }
        ),
        { body: JSON.stringify({ condition }) }
      ),
    clearRolePermissionCondition: (id: string, code: string) =>
      request(
        apiRoute(
          "delete",
          "/roles/{role_id}/permissions/{permission_code}/condition",
          { path: { role_id: id, permission_code: code } }
        )
      ),
    roleMembers: (id: string) =>
      requestAllPages<string>(
        pagedRoute("/roles/{role_id}/members", { path: { role_id: id } })
      ),
    addRoleMember: (id: string, subject: string) =>
      request(
        apiRoute("put", "/roles/{role_id}/members/{subject}", {
          path: { role_id: id, subject: subject },
        })
      ),
    removeRoleMember: (id: string, subject: string) =>
      request(
        apiRoute("delete", "/roles/{role_id}/members/{subject}", {
          path: { role_id: id, subject: subject },
        })
      ),
    roleDirectoryBindings: (id: string) =>
      request<RoleDirectoryBinding[]>(
        apiRoute("get", "/roles/{role_id}/directory-bindings", {
          path: { role_id: id },
        })
      ),
    roleDataScope: (id: string) =>
      request<RoleDataScope>(
        apiRoute("get", "/roles/{role_id}/data-scope", {
          path: { role_id: id },
        })
      ),
    setRoleDataScope: (
      id: string,
      body: Pick<RoleDataScope, "scope" | "department_ids">
    ) =>
      request<RoleDataScope>(
        apiRoute("put", "/roles/{role_id}/data-scope", {
          path: { role_id: id },
        }),
        { body: JSON.stringify(body) }
      ),
    addRoleDirectoryBinding: (
      id: string,
      assigneeType: RoleDirectoryBinding["assignee_type"],
      assigneeID: string
    ) =>
      request(
        apiRoute(
          "put",
          "/roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}",
          {
            path: {
              role_id: id,
              assignee_type: assigneeType,
              assignee_id: assigneeID,
            },
          }
        )
      ),
    removeRoleDirectoryBinding: (
      id: string,
      assigneeType: RoleDirectoryBinding["assignee_type"],
      assigneeID: string
    ) =>
      request(
        apiRoute(
          "delete",
          "/roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}",
          {
            path: {
              role_id: id,
              assignee_type: assigneeType,
              assignee_id: assigneeID,
            },
          }
        )
      ),
    permissions: () =>
      request<Permission[]>(
        apiRoute("get", "/tenants/{tenant_id}/permission-catalog", {
          path: { tenant_id: tenantID() },
        })
      ),
    menus: () =>
      request<Menu[]>(
        apiRoute("get", "/tenants/{tenant_id}/menus", {
          path: { tenant_id: tenantID() },
        })
      ),
    effectiveMenus: async (tenantID: string) =>
      (await request<EffectiveMenu[] | null>(
        apiRoute(
          "get",
          "/tenants/{tenant_id}/principals/{principal_id}/menus",
          {
            path: { principal_id: await principalID(), tenant_id: tenantID },
          }
        )
      )) ?? [],
    myPermissions: async (tenantID?: string, entityID?: string) =>
      (await request<string[] | null>(
        tenantID && entityID
          ? apiRoute(
              "get",
              "/tenants/{tenant_id}/entities/{entity_id}/principals/{principal_id}/permissions",
              {
                path: {
                  principal_id: await principalID(),
                  tenant_id: tenantID,
                  entity_id: entityID,
                },
              }
            )
          : tenantID
            ? apiRoute(
                "get",
                "/tenants/{tenant_id}/principals/{principal_id}/permissions",
                {
                  path: {
                    principal_id: await principalID(),
                    tenant_id: tenantID,
                  },
                }
              )
            : apiRoute("get", "/principals/{principal_id}/permissions", {
                path: { principal_id: await principalID() },
              })
      )) ?? [],
    createMenu: (body: MenuInput) =>
      request<Menu>(
        apiRoute("post", "/tenants/{tenant_id}/menus", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    updateMenu: (id: string, body: Partial<MenuInput>) =>
      request<Menu>(
        apiRoute("patch", "/menus/{menu_id}", { path: { menu_id: id } }),
        { body: JSON.stringify(body) }
      ),
    deleteMenu: (id: string) =>
      request(
        apiRoute("delete", "/menus/{menu_id}", { path: { menu_id: id } })
      ),
    oauthClients: () =>
      requestAllPages<OAuthClientResponse>(
        pagedRoute("/tenants/{tenant_id}/oauth-clients", {
          path: { tenant_id: tenantID() },
        })
      ).then((clients) => clients.map(normalizeOAuthClient)),
    createOAuthClient: (body: OAuthClientInput) =>
      request<
        Omit<OAuthClientCreateResult, "client"> & {
          client: OAuthClientResponse
        }
      >(
        apiRoute("post", "/tenants/{tenant_id}/oauth-clients", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ).then((result) => ({
        ...result,
        client: normalizeOAuthClient(result.client),
      })),
    updateOAuthClient: (
      id: string,
      body: OAuthClientInput & Pick<OAuthClient, "status">
    ) =>
      request<OAuthClientResponse>(
        apiRoute("put", "/oauth-clients/{client_id}", {
          path: { client_id: id },
        }),
        { body: JSON.stringify(body) }
      ).then(normalizeOAuthClient),
    rotateOAuthClientSecret: (id: string) =>
      request<{ client_secret: string }>(
        apiRoute("post", "/oauth-clients/{client_id}/credentials", {
          path: { client_id: id },
        })
      ),
    deleteOAuthClient: (id: string) =>
      request<{ deleted: boolean }>(
        apiRoute("delete", "/oauth-clients/{client_id}", {
          path: { client_id: id },
        })
      ),
    scimDirectories: () =>
      requestAllPages<SCIMDirectory>(
        pagedRoute("/tenants/{tenant_id}/scim-directories", {
          path: { tenant_id: tenantID() },
        })
      ),
    scimTargets: () =>
      requestAllPages<SCIMTarget>(
        pagedRoute("/tenants/{tenant_id}/scim-targets", {
          path: { tenant_id: tenantID() },
        })
      ),
    createSCIMTarget: (body: SCIMTargetInput) =>
      request<SCIMTargetSecret>(
        apiRoute("post", "/tenants/{tenant_id}/scim-targets", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    updateSCIMTarget: (id: string, body: SCIMTargetUpdate) =>
      request<SCIMTarget>(
        apiRoute("put", "/scim-targets/{target_id}", {
          path: { target_id: id },
        }),
        { body: JSON.stringify(body) }
      ),
    deleteSCIMTarget: (id: string) =>
      request<{ deleted: boolean }>(
        apiRoute("delete", "/scim-targets/{target_id}", {
          path: { target_id: id },
        })
      ),
    pushSCIMResource: (id: string, body: SCIMResourceInput) =>
      request<SCIMPushResult>(
        apiRoute("post", "/scim-targets/{target_id}/provisioning-jobs", {
          path: { target_id: id },
        }),
        { body: JSON.stringify(body) }
      ),
    deprovisionSCIMResource: (id: string, body: SCIMResourceInput) =>
      request<{ deprovisioned: boolean }>(
        apiRoute("post", "/scim-targets/{target_id}/deprovisioning-jobs", {
          path: { target_id: id },
        }),
        { body: JSON.stringify(body) }
      ),
    retireSCIMTarget: (id: string) =>
      request<{ deprovisioned: number }>(
        apiRoute("put", "/scim-targets/{target_id}/retirement", {
          path: { target_id: id },
        })
      ),
    createSCIMDirectory: (body: SCIMDirectoryInput) =>
      request<SCIMDirectory>(
        apiRoute("post", "/tenants/{tenant_id}/scim-directories", {
          path: { tenant_id: tenantID() },
        }),
        { body: JSON.stringify(body) }
      ),
    updateSCIMDirectory: (
      id: string,
      body: SCIMDirectoryInput & Pick<SCIMDirectory, "status" | "version">
    ) =>
      request<SCIMDirectory>(
        apiRoute("put", "/scim-directories/{directory_id}", {
          path: { directory_id: id },
        }),
        { body: JSON.stringify(body) }
      ),
    scimCredentials: (directoryID: string) =>
      requestAllPages<SCIMCredential>(
        pagedRoute("/scim-directories/{directory_id}/credentials", {
          path: { directory_id: directoryID },
        })
      ),
    createSCIMCredential: (
      directoryID: string,
      body: { name: string; expires_at?: string }
    ) =>
      request<SCIMCredentialSecret>(
        apiRoute("post", "/scim-directories/{directory_id}/credentials", {
          path: { directory_id: directoryID },
        }),
        { body: JSON.stringify(body) }
      ),
    revokeSCIMCredential: (directoryID: string, credentialID: string) =>
      request<{ revoked: boolean }>(
        apiRoute(
          "delete",
          "/scim-directories/{directory_id}/credentials/{credential_id}",
          { path: { directory_id: directoryID, credential_id: credentialID } }
        )
      ),
    auditEvents: async (
      query: RouteQuery<"/tenants/{tenant_id}/audit/events"> = {}
    ) => {
      const envelope = await requestEnvelope<AuditEvent[]>(
        apiRoute("get", "/tenants/{tenant_id}/audit/events", {
          path: { tenant_id: tenantID() },
          query,
        })
      )
      return {
        items: envelope.data,
        total: envelope.meta?.page?.total ?? envelope.data.length,
      }
    },
    auditEvent: (id: string) =>
      request<AuditEvent>(
        apiRoute("get", "/tenants/{tenant_id}/audit/events/{event_id}", {
          path: { tenant_id: tenantID(), event_id: id },
        })
      ),
    auditIntegrity: () =>
      request<AuditIntegrity>(
        apiRoute("get", "/tenants/{tenant_id}/audit/integrity-verifications", {
          path: { tenant_id: tenantID() },
        })
      ),
    auditExport: (
      query: RouteQuery<"/tenants/{tenant_id}/audit/exports"> = {}
    ) =>
      requestAuditDownloadWithBase(
        apiBase,
        apiRoute("get", "/tenants/{tenant_id}/audit/exports", {
          path: { tenant_id: tenantID() },
          query,
        })
      ),
    auditArchiveStatus: (scope: AuditScope = "tenant") =>
      request<AuditArchiveStatus>(
        scope === "platform"
          ? apiRoute("get", "/platform/audit/archive-status")
          : apiRoute("get", "/tenants/{tenant_id}/audit/archive-status", {
              path: { tenant_id: tenantID() },
            })
      ),
    auditArchives: async (
      offset = 0,
      limit = 20,
      scope: AuditScope = "tenant"
    ) => {
      const query = { offset, limit }
      const envelope = await requestEnvelope<AuditArchiveJob[]>(
        scope === "platform"
          ? apiRoute("get", "/platform/audit/archives", { query })
          : apiRoute("get", "/tenants/{tenant_id}/audit/archives", {
              path: { tenant_id: tenantID() },
              query,
            })
      )
      return {
        items: envelope.data,
        total: envelope.meta?.page?.total ?? envelope.data.length,
      }
    },
    requestAuditArchive: (scope: AuditScope = "tenant") =>
      request<AuditArchiveJob>(
        scope === "platform"
          ? apiRoute("post", "/platform/audit/archives")
          : apiRoute("post", "/tenants/{tenant_id}/audit/archives", {
              path: { tenant_id: tenantID() },
            })
      ),
    retryAuditArchive: (id: string, scope: AuditScope = "tenant") =>
      request<AuditArchiveJob>(
        scope === "platform"
          ? apiRoute("post", "/platform/audit/archives/{archive_id}/attempts", {
              path: { archive_id: id },
            })
          : apiRoute(
              "post",
              "/tenants/{tenant_id}/audit/archives/{archive_id}/attempts",
              { path: { tenant_id: tenantID(), archive_id: id } }
            )
      ),
    verifyAuditArchive: (id: string, scope: AuditScope = "tenant") =>
      request<AuditArchiveObject>(
        scope === "platform"
          ? apiRoute(
              "post",
              "/platform/audit/archives/{archive_id}/verifications",
              { path: { archive_id: id } }
            )
          : apiRoute(
              "post",
              "/tenants/{tenant_id}/audit/archives/{archive_id}/verifications",
              { path: { tenant_id: tenantID(), archive_id: id } }
            )
      ),
    downloadAuditArchive: (id: string, scope: AuditScope = "tenant") =>
      requestAuditDownloadWithBase(
        apiBase,
        scope === "platform"
          ? apiRoute("get", "/platform/audit/archives/{archive_id}/content", {
              path: { archive_id: id },
            })
          : apiRoute(
              "get",
              "/tenants/{tenant_id}/audit/archives/{archive_id}/content",
              { path: { tenant_id: tenantID(), archive_id: id } }
            )
      ),
    auditGovernance: () =>
      request<AuditGovernance>(
        apiRoute("get", "/tenants/{tenant_id}/audit/governance-policy", {
          path: { tenant_id: tenantID() },
        })
      ),
    setAuditRetention: (
      body: Pick<AuditRetentionPolicy, "min_days" | "archive_after_days">,
      scope: AuditScope = "tenant"
    ) =>
      request<AuditRetentionPolicy>(
        scope === "platform"
          ? apiRoute("put", "/platform/audit/retention-policy")
          : apiRoute("put", "/tenants/{tenant_id}/audit/retention-policy", {
              path: { tenant_id: tenantID() },
            }),
        { body: JSON.stringify(body) }
      ),
    auditSignRoot: () =>
      request<AuditAnchor>(
        apiRoute("post", "/tenants/{tenant_id}/audit/root-signatures", {
          path: { tenant_id: tenantID() },
        })
      ),
  }
}

export type IamApi = ReturnType<typeof createIamApi>
export const iamApi = createIamApi()
