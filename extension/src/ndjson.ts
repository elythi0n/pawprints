/** Splits a stream of chunks into complete lines, keeping any partial tail. */
export class LineSplitter {
  private rest = "";

  push(chunk: string): string[] {
    const text = this.rest + chunk;
    const lines = text.split("\n");
    this.rest = lines.pop() ?? "";
    return lines.map((l) => l.replace(/\r$/, "")).filter((l) => l.length > 0);
  }

  flush(): string | undefined {
    const tail = this.rest.trim();
    this.rest = "";
    return tail.length > 0 ? tail : undefined;
  }
}

export function parseJsonLine(line: string): unknown | undefined {
  const trimmed = line.trim();
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) {
    return undefined;
  }
  try {
    return JSON.parse(trimmed);
  } catch {
    return undefined;
  }
}

export function parseJsonLines(text: string): unknown[] {
  const out: unknown[] = [];
  for (const line of text.split("\n")) {
    const value = parseJsonLine(line);
    if (value !== undefined) {
      out.push(value);
    }
  }
  return out;
}

export interface ErrorLine {
  message: string;
  exit: number;
}

/** The binary reports failures as {"error": "...", "exit": N} on stdout. */
export function errorOf(value: unknown): ErrorLine | undefined {
  if (typeof value !== "object" || value === null) {
    return undefined;
  }
  const obj = value as Record<string, unknown>;
  if (typeof obj.error !== "string") {
    return undefined;
  }
  const exit = typeof obj.exit === "number" ? obj.exit : 1;
  return { message: obj.error, exit };
}
