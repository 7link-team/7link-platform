/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Browser API base path. Defaults to /api. */
  readonly VITE_API_URL?: string
  /** Local backend used by the Vite /api proxy. */
  readonly VITE_API_PROXY_TARGET?: string
  /** Local-development login personas. Ignored by non-development builds. */
  readonly VITE_DEV_LOGIN_ACCOUNTS?: string
  /** Explicitly expose configured login personas in the development UI. */
  readonly VITE_DEV_LOGIN_ACCOUNTS_ENABLED?: string
  /** Fill the first development persona into the login form on load. */
  readonly VITE_DEV_LOGIN_PREFILL?: string
  /** Console display name shown in the chrome. Defaults to a neutral "IAM". */
  readonly VITE_APP_NAME?: string
  /** Organization named in the footer. Omitted means the footer shows only the year. */
  readonly VITE_ORG_NAME?: string
  /** Google Maps JS API key for the map picker component (was NEXT_PUBLIC_GOOGLE_MAPS_API_KEY). */
  readonly VITE_GOOGLE_MAPS_API_KEY?: string
  /** Mapbox access token for the map picker component (was NEXT_PUBLIC_MAPBOX_TOKEN). */
  readonly VITE_MAPBOX_TOKEN?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
