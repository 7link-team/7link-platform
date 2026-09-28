// Ties the hand-written types in iam-api.ts to the service's OpenAPI document.
//
// The types in iam-api.ts stay hand-written on purpose: they say
// `status: "active" | "disabled"` where the document says `string`, and they
// leave `id` non-nullable where the document says `string | null` because the
// service never sends a null id. Replacing them with the generated types would
// trade that precision for a lot of null checks that can never fire.
//
// What was missing was any way to notice the service moving underneath them.
// This file is that: it compares field names in both directions and fails the
// typecheck when they disagree. Field types are deliberately not compared,
// since the two sides differ on purpose as described above.
//
// A failure reads like
//
//   Type '{ fieldsTheServiceDoesNotHave: "sign_count"; ... }' does not satisfy
//   the constraint '{ fieldsTheServiceDoesNotHave: never; ... }'
//
// which names the field that moved. Regenerate api-schema.ts first
// (`bun run generate:api-types`), then decide whether the client or the service
// is the one that is wrong.

import type { components, operations } from "./api-schema"
import type {
  iamApi,
  AccessRequest,
  AccessReview,
  AccessReviewItem,
  AuditAnchor,
  AuditArchiveJob,
  AuditArchiveObject,
  AuditArchiveStatus,
  AuditEvent,
  AuditGovernance,
  AuditIntegrity,
  AuditRetentionPolicy,
  AuthnCapabilities,
  AuthorizationExplanation,
  BrowserSession,
  Captcha,
  DataConstraint,
  Department,
  Entity,
  EntityRoleBinding,
  FieldDefinition,
  FieldAccess,
  FieldPolicy,
  ManagedFieldPolicy,
  FieldConstraint,
  Group,
  GroupMember,
  Invitation,
  InvitationAcceptance,
  IssuedInvitation,
  LoginResult,
  Member,
  Menu,
  MfaConfirmation,
  MfaEnrollment,
  MfaStatus,
  OAuthClient,
  OAuthClientCreateResult,
  Passkey,
  PasskeyOptions,
  Permission,
  Principal,
  Relationship,
  RequestableRole,
  Role,
  RoleDataScope,
  RoleDirectoryBinding,
  RolePermissionGrant,
  SCIMCredential,
  SCIMCredentialSecret,
  SCIMDirectory,
  SCIMTarget,
  SCIMTargetInput,
  SCIMTargetUpdate,
  SCIMTargetSecret,
  SCIMResourceInput,
  SCIMPushResult,
  ServiceAccount,
  ServiceAccountCredential,
  ServiceAccountCredentialSecret,
  Session,
  Tenant,
} from "./iam-api"

type S = components["schemas"]

type RequiredKeys<T> = {
  [K in keyof T]-?: object extends Pick<T, K> ? never : K
}[keyof T]

// Resolves to true when the two agree, and otherwise to an object naming the
// fields that do not. The naming is the whole point: the compiler prints this
// type in the failure, so the message says which field moved.
type Compare<Client, Spec> = [Exclude<keyof Client, keyof Spec>] extends [never]
  ? [Exclude<RequiredKeys<Spec>, keyof Client>] extends [never]
    ? true
    : {
        requiredFieldsTheClientOmits: Exclude<RequiredKeys<Spec>, keyof Client>
      }
  : { fieldsTheServiceDoesNotHave: Exclude<keyof Client, keyof Spec> }

type Agreed<R extends true> = R

type Optionality<Client, Spec> = [
  Exclude<RequiredKeys<Client>, RequiredKeys<Spec>>,
] extends [never]
  ? true
  : {
      clientRequiresOptionalFields: Exclude<
        RequiredKeys<Client>,
        RequiredKeys<Spec>
      >
    }

export type _FieldDefinition = Agreed<Compare<FieldDefinition, S["Field"]>>
export type _FieldAccess = Agreed<Compare<FieldAccess, S["FieldAccess"]>>
export type _FieldPolicy = Agreed<Compare<FieldPolicy, S["FieldPolicy"]>>
export type _ManagedFieldPolicy = Agreed<
  Compare<ManagedFieldPolicy, S["ManagedFieldPolicy"]>
>
export type _FieldConstraint = Agreed<
  Compare<FieldConstraint, S["FieldConstraint"]>
>
export type _FieldPolicyInput = Agreed<
  Compare<{ policy: FieldPolicy; version: number }, S["FieldPolicyInputBody"]>
>

export type _AccessRequest = Agreed<Compare<AccessRequest, S["AccessRequest"]>>
export type _AccessReview = Agreed<Compare<AccessReview, S["AccessReview"]>>
export type _AccessReviewItem = Agreed<
  Compare<AccessReviewItem, S["AccessReviewItem"]>
>
export type _AuditAnchor = Agreed<Compare<AuditAnchor, S["Anchor"]>>
export type _AuditArchiveJob = Agreed<Compare<AuditArchiveJob, S["ArchiveJob"]>>
export type _AuditArchiveObject = Agreed<
  Compare<AuditArchiveObject, S["ArchiveObject"]>
>
export type _AuditArchiveStatus = Agreed<
  Compare<AuditArchiveStatus, S["ArchiveStatus"]>
>
export type _AuditEvent = Agreed<Compare<AuditEvent, S["Event"]>>
export type _AuditGovernance = Agreed<Compare<AuditGovernance, S["Governance"]>>
export type _AuditIntegrity = Agreed<Compare<AuditIntegrity, S["Integrity"]>>
export type _AuditRetentionPolicy = Agreed<
  Compare<AuditRetentionPolicy, S["RetentionPolicy"]>
>
export type _AuthnCapabilities = Agreed<
  Compare<AuthnCapabilities, S["AuthenticationCapabilities"]>
>
export type _AuthorizationExplanation = Agreed<
  Compare<AuthorizationExplanation, S["Explanation"]>
>
export type _BrowserSession = Agreed<
  Compare<BrowserSession, S["BrowserSession"]>
>
export type _Captcha = Agreed<Compare<Captcha, S["CaptchaOutput"]>>
export type _DataConstraint = Agreed<
  Compare<DataConstraint, S["DataConstraint"]>
>
export type _Department = Agreed<Compare<Department, S["Department"]>>
export type _Entity = Agreed<Compare<Entity, S["APIEntity"]>>
export type _EntityRoleBinding = Agreed<
  Compare<EntityRoleBinding, S["APIEntityRoleBinding"]>
>
export type _Group = Agreed<Compare<Group, S["Group"]>>
export type _GroupMember = Agreed<Compare<GroupMember, S["GroupMember"]>>
export type _Invitation = Agreed<Compare<Invitation, S["Invitation"]>>
export type _InvitationAcceptance = Agreed<
  Compare<InvitationAcceptance, S["InvitationAcceptance"]>
>
export type _IssuedInvitation = Agreed<
  Compare<IssuedInvitation, S["IssuedInvitation"]>
>
export type _LoginResult = Agreed<Compare<LoginResult, S["LoginResult"]>>
export type _Member = Agreed<Compare<Member, S["APITenantMember"]>>
export type _SystemMemberRoleIDs = Agreed<
  Awaited<
    ReturnType<typeof iamApi.systemMemberRoles>
  >[number] extends NonNullable<
    NonNullable<
      operations["iam-system-member-roles"]["responses"][200]["content"]["application/json"]["data"]
    >[number]
  >
    ? true
    : false
>
export type _Menu = Agreed<Compare<Menu, S["APIMenu"]>>
export type _MfaConfirmation = Agreed<
  Compare<MfaConfirmation, S["MFAConfirmation"]>
>
export type _MfaEnrollment = Agreed<Compare<MfaEnrollment, S["MFAEnrollment"]>>
export type _MfaStatus = Agreed<Compare<MfaStatus, S["MFAStatus"]>>
export type _OAuthClient = Agreed<Compare<OAuthClient, S["Client"]>>
export type _OAuthClientCreateResult = Agreed<
  Compare<OAuthClientCreateResult, S["ClientCreateData"]>
>
export type _Passkey = Agreed<Compare<Passkey, S["Passkey"]>>

// One client type covers both passkey ceremonies because the two documents
// differ only in the nested `options` member, which the client hands straight
// to the browser without reading it.
export type _PasskeyCreationOptions = Agreed<
  Compare<PasskeyOptions, S["PasskeyCreationOptions"]>
>
export type _PasskeyAssertionOptions = Agreed<
  Compare<PasskeyOptions, S["PasskeyAssertionOptions"]>
>

export type _Permission = Agreed<Compare<Permission, S["Action"]>>

export type _Principal = Agreed<Compare<Principal, S["PrincipalView"]>>
export type _PrincipalOptional = Agreed<
  Optionality<Principal, S["PrincipalView"]>
>
export type _MemberOptional = Agreed<Optionality<Member, S["APITenantMember"]>>
export type _SessionOptional = Agreed<Optionality<Session, S["MeOutput"]>>
export type _Relationship = Agreed<Compare<Relationship, S["APIRelationship"]>>
export type _RequestableRole = Agreed<
  Compare<RequestableRole, S["RequestableRole"]>
>
export type _Role = Agreed<Compare<Role, S["APIRole"]>>
export type _RoleDataScope = Agreed<
  Compare<RoleDataScope, S["APIRoleDataScope"]>
>
export type _RoleDirectoryBinding = Agreed<
  Compare<RoleDirectoryBinding, S["APIRoleDirectoryBinding"]>
>
export type _RolePermissionGrant = Agreed<
  Compare<RolePermissionGrant, S["APIRolePermissionGrant"]>
>
export type _SCIMCredential = Agreed<Compare<SCIMCredential, S["Credential"]>>
export type _SCIMCredentialSecret = Agreed<
  Compare<SCIMCredentialSecret, S["CredentialSecret"]>
>
export type _SCIMDirectory = Agreed<Compare<SCIMDirectory, S["Directory"]>>
export type _SCIMTarget = Agreed<Compare<SCIMTarget, S["Target"]>>
export type _SCIMTargetInput = Agreed<
  Compare<SCIMTargetInput, S["TargetCreateInputBody"]>
>
export type _SCIMTargetUpdate = Agreed<
  Compare<SCIMTargetUpdate, S["TargetReplaceInputBody"]>
>
export type _SCIMTargetSecret = Agreed<
  Compare<SCIMTargetSecret, S["TargetSecret"]>
>
export type _SCIMResourceInput = Agreed<
  Compare<SCIMResourceInput, S["TargetPushInputBody"]>
>
export type _SCIMPushResult = Agreed<Compare<SCIMPushResult, S["PushResult"]>>
export type _ServiceAccount = Agreed<
  Compare<ServiceAccount, S["ServiceAccount"]>
>
export type _ServiceAccountCredential = Agreed<
  Compare<ServiceAccountCredential, S["ServiceAccountCredential"]>
>
export type _ServiceAccountCredentialSecret = Agreed<
  Compare<ServiceAccountCredentialSecret, S["ServiceAccountCredentialSecret"]>
>
export type _Session = Agreed<Compare<Session, S["MeOutput"]>>
export type _Tenant = Agreed<Compare<Tenant, S["Tenant"]>>
