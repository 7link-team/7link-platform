import { Fragment, type ReactNode } from "react"

// Render message markdown as React text nodes. Raw HTML is always text and
// links are limited to web URLs; no user HTML enters the DOM.
function inline(text: string): ReactNode[] {
  return text
    .split(/(`[^`]+`|\*\*[^*]+\*\*|\[[^\]]+\]\(https?:\/\/[^\s)]+\))/g)
    .map((part, index) => {
      if (part.startsWith("`"))
        return (
          <code key={index} className="rounded bg-muted px-1 font-mono text-sm">
            {part.slice(1, -1)}
          </code>
        )
      if (part.startsWith("**"))
        return <strong key={index}>{part.slice(2, -2)}</strong>
      const link = /^\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)$/.exec(part)
      if (link)
        return (
          <a
            key={index}
            href={link[2]}
            target="_blank"
            rel="noopener noreferrer"
            className="text-primary underline"
          >
            {link[1]}
          </a>
        )
      return <Fragment key={index}>{part}</Fragment>
    })
}

export function ConversationMarkdown({ body }: { body: string | null }) {
  const blocks = (body ?? "").split(/(```[^\n]*\n[\s\S]*?```)/g)
  return (
    <div className="min-w-0 space-y-2 text-sm leading-6 break-words">
      {blocks.map((block, index) => {
        if (block.startsWith("```"))
          return (
            <pre
              key={index}
              className="overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs"
            >
              <code>
                {block.replace(/^```[^\n]*\n/, "").replace(/```$/, "")}
              </code>
            </pre>
          )
        return (
          <div key={index}>
            {block.split("\n").map((line, i) => {
              if (/^#{1,3} /.test(line))
                return (
                  <p key={i} className="font-semibold">
                    {inline(line.replace(/^#{1,3} /, ""))}
                  </p>
                )
              if (line.startsWith("- "))
                return (
                  <p key={i} className="ps-3">
                    • {inline(line.slice(2))}
                  </p>
                )
              return (
                <p key={i} className="min-h-6 whitespace-pre-wrap">
                  {inline(line)}
                </p>
              )
            })}
          </div>
        )
      })}
    </div>
  )
}
