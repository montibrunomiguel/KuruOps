import type { JSX, ReactNode } from "react";

// A small, dependency-free renderer for the subset of markdown an LLM
// analysis response typically uses (headers, bold/italic, inline code,
// fenced code blocks, bullet/numbered lists, paragraphs). Deliberately not
// a full CommonMark implementation -- it builds React elements directly
// instead of parsing to an HTML string, so AI-generated text never gets a
// dangerouslySetInnerHTML/HTML-injection surface.
export function renderMarkdown(text: string): ReactNode {
  const lines = text.split("\n");
  const blocks: ReactNode[] = [];
  let i = 0;
  let key = 0;

  while (i < lines.length) {
    const line = lines[i];

    if (line.trim() === "") {
      i++;
      continue;
    }

    if (line.trim().startsWith("```")) {
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith("```")) {
        codeLines.push(lines[i]);
        i++;
      }
      i++; // skip closing fence (or end of text if it was never closed)
      blocks.push(
        <pre key={key++} className="mono" style={{ margin: "4px 0", fontSize: 12, whiteSpace: "pre-wrap", overflowX: "auto" }}>
          <code>{codeLines.join("\n")}</code>
        </pre>,
      );
      continue;
    }

    const headerMatch = /^(#{1,6})\s+(.*)$/.exec(line);
    if (headerMatch) {
      const level = Math.min(headerMatch[1].length + 2, 6);
      const Tag = `h${level}` as keyof JSX.IntrinsicElements;
      blocks.push(
        <Tag key={key++} style={{ margin: "6px 0 2px", fontSize: level <= 4 ? 14.5 : 13 }}>
          {renderInline(headerMatch[2], `${key}`)}
        </Tag>,
      );
      i++;
      continue;
    }

    const bulletMatch = /^\s*[-*]\s+(.*)$/.exec(line);
    const orderedMatch = /^\s*\d+\.\s+(.*)$/.exec(line);
    if (bulletMatch || orderedMatch) {
      const ordered = !!orderedMatch;
      const items: string[] = [];
      while (i < lines.length) {
        const m = ordered ? /^\s*\d+\.\s+(.*)$/.exec(lines[i]) : /^\s*[-*]\s+(.*)$/.exec(lines[i]);
        if (!m) break;
        items.push(m[1]);
        i++;
      }
      const ListTag = ordered ? "ol" : "ul";
      blocks.push(
        <ListTag key={key++} style={{ margin: "4px 0", paddingLeft: 20 }}>
          {items.map((item, idx) => (
            <li key={idx}>{renderInline(item, `${key}-${idx}`)}</li>
          ))}
        </ListTag>,
      );
      continue;
    }

    const paraLines: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !lines[i].trim().startsWith("```") &&
      !/^#{1,6}\s+/.test(lines[i]) &&
      !/^\s*[-*]\s+/.test(lines[i]) &&
      !/^\s*\d+\.\s+/.test(lines[i])
    ) {
      paraLines.push(lines[i]);
      i++;
    }
    blocks.push(
      <p key={key++} style={{ margin: "4px 0" }}>
        {renderInline(paraLines.join(" "), `${key}`)}
      </p>,
    );
  }

  return <>{blocks}</>;
}

function renderInline(text: string, keyPrefix: string): ReactNode[] {
  const tokens = text.split(/(\*\*[^*]+\*\*|`[^`]+`|\*[^*]+\*|_[^_]+_)/g);
  return tokens
    .filter((t) => t !== "")
    .map((token, idx) => {
      const k = `${keyPrefix}-${idx}`;
      if (token.startsWith("**") && token.endsWith("**") && token.length > 4) {
        return <strong key={k}>{token.slice(2, -2)}</strong>;
      }
      if (token.startsWith("`") && token.endsWith("`") && token.length > 2) {
        return (
          <code key={k} className="mono" style={{ fontSize: "0.92em" }}>
            {token.slice(1, -1)}
          </code>
        );
      }
      if (
        (token.startsWith("*") && token.endsWith("*") && token.length > 2) ||
        (token.startsWith("_") && token.endsWith("_") && token.length > 2)
      ) {
        return <em key={k}>{token.slice(1, -1)}</em>;
      }
      return <span key={k}>{token}</span>;
    });
}
