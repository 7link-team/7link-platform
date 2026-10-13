import type { paths } from "./api-schema"

type Method = "get" | "post" | "put" | "patch" | "delete"
type Supported<P extends keyof paths> = {
  [M in Method]: NonNullable<paths[P][M]> extends never ? never : M
}[Method]
type Parameters<P extends keyof paths, M extends Supported<P>> =
  NonNullable<paths[P][M]> extends { parameters: infer V } ? V : never
export type RouteQuery<
  P extends keyof paths,
  M extends Supported<P> = "get" & Supported<P>,
> = Parameters<P, M> extends { query?: infer Q } ? NonNullable<Q> : never
type Options<P extends keyof paths, M extends Supported<P>> = Pick<
  Parameters<P, M>,
  ("path" & keyof Parameters<P, M>) | ("query" & keyof Parameters<P, M>)
>
const routeBrand: unique symbol = Symbol("api-route")
export type ApiRoute = {
  readonly [routeBrand]: true
  readonly url: string
  readonly method: Uppercase<Method>
}
export type PagedRoute = ApiRoute & {
  readonly page: (offset: number, limit: number) => ApiRoute
}
type PagedPath = {
  [P in keyof paths]: [RouteQuery<P>] extends [never]
    ? never
    : "offset" | "limit" extends keyof RouteQuery<P>
      ? P
      : never
}[keyof paths]

export function apiRoute<P extends keyof paths, M extends Supported<P>>(
  method: M,
  template: P,
  ...args: Record<never, never> extends Options<P, M>
    ? [options?: Options<P, M>]
    : [options: Options<P, M>]
): ApiRoute {
  const options = args[0] as { path?: object; query?: object } | undefined
  const values = options?.path as Record<string, unknown> | undefined
  const url = template.replace(/\{([^}]+)\}/g, (_, name: string) => {
    const value = String(values?.[name])
    // This OpenAPI parameter is a slash-separated relative file path.
    return template === "/files/{root_file_id}/-/{path}" && name === "path"
      ? value.split("/").map(encodeURIComponent).join("/")
      : encodeURIComponent(value)
  })
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(options?.query ?? {}))
    if (value !== undefined) query.set(key, String(value))
  return {
    [routeBrand]: true,
    method: method.toUpperCase() as Uppercase<Method>,
    url: `${url}${query.size ? `?${query}` : ""}`,
  }
}

export function pagedRoute<P extends PagedPath>(
  template: P,
  ...args: Record<never, never> extends Options<P, "get" & Supported<P>>
    ? [options?: Options<P, "get" & Supported<P>>]
    : [options: Options<P, "get" & Supported<P>>]
): PagedRoute {
  // The public signature admits only GET operations with offset/limit queries.
  const build = apiRoute as (
    method: "get",
    template: P,
    options?: { path?: object; query?: object }
  ) => ApiRoute
  const options = args[0] as { path?: object; query?: object } | undefined
  return {
    ...build("get", template, options),
    page: (offset, limit) =>
      build("get", template, {
        ...options,
        query: { ...options?.query, offset, limit },
      }),
  }
}
