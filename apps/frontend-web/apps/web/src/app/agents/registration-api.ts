import type { components } from "../../lib/api-schema"

type Creation = components["schemas"]["AgentCreation"]
type AvatarStyle = "PIXEL" | "ROBOT" | "MONSTER" | "GEOMETRIC"

export function buildAgentCreation(
  values: Record<string, unknown>,
  machine: string,
  runtime: string
): Creation {
  return {
    machine_id: machine,
    runtime: runtime as Creation["runtime"],
    name: String(values.name),
    handle: String(values.handle),
    persona: String(values.persona ?? ""),
    model: String(values.model),
    roles: values.roles as Creation["roles"],
    authorization_mode:
      values.authorization_mode as Creation["authorization_mode"],
    system_prompt: String(values.system_prompt),
    avatar_style: values.avatar_style as AvatarStyle,
    avatar_seed: Number(values.avatar_seed),
    skill_ids: (values.skill_ids ?? []) as string[],
    memory_policy: JSON.stringify(
      typeof values.memory_policy === "string"
        ? JSON.parse(values.memory_policy)
        : values.memory_policy
    ),
    default_channels: (Array.isArray(values.default_channels)
      ? values.default_channels
      : []
    )
      .map((value: unknown) => String(value).trim())
      .filter(Boolean),
    ...(values.reasoning_effort
      ? {
          reasoning_effort:
            values.reasoning_effort as Creation["reasoning_effort"],
        }
      : {}),
  }
}
