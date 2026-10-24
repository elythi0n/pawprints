import { formatNumber } from "./format";
import { Stats, Usage } from "./types";

type Row = string[];

function table(headers: string[], rows: Row[], rightAlign: boolean[] = []): string {
  const widths = headers.map((h, i) => Math.max(h.length, ...rows.map((r) => (r[i] ?? "").length)));
  const line = (cells: string[]) =>
    cells.map((c, i) => (rightAlign[i] ? c.padStart(widths[i]) : c.padEnd(widths[i]))).join("  ").trimEnd();
  const out = [line(headers), widths.map((w) => "-".repeat(w)).join("  ")];
  for (const row of rows) {
    out.push(line(row));
  }
  return out.join("\n");
}

function usageRow(name: string, u: Usage): Row {
  return [
    name,
    formatNumber(u.input),
    formatNumber(u.output),
    formatNumber(u.cache_read),
    formatNumber(u.cache_write),
    formatNumber(u.thinking),
    formatNumber(u.messages),
  ];
}

/** Plain-text rendering of `stats --json` for the read-only stats document. */
export function renderStats(stats: Stats, title = "Pawprints Stats"): string {
  const sections: string[] = [];
  const scope = stats.since ? `since ${stats.since.slice(0, 10)}` : "all time";
  sections.push(`${title}\n${"=".repeat(title.length)}\n${scope}, ${stats.projects} project${stats.projects === 1 ? "" : "s"}`);

  sections.push(
    table(
      ["Metric", "Value"],
      [
        ["Sessions", formatNumber(stats.sessions)],
        ["Live sessions", formatNumber(stats.live_sessions)],
        ["Turns", formatNumber(stats.turns)],
        ["Edits", formatNumber(stats.edits)],
        ["Changed outside Claude", formatNumber(stats.external_edits)],
        ["Files", formatNumber(stats.files)],
        ["Lines added", formatNumber(stats.lines_added)],
        ["Lines removed", formatNumber(stats.lines_removed)],
        ...(stats.cost_estimate !== undefined
          ? [["API cost (USD)", `${stats.cost_estimate.toFixed(2)}${stats.cost_complete === false ? " (some models have no price)" : ""}`]]
          : []),
      ],
      [false, true]
    )
  );

  const models = Object.entries(stats.usage ?? {});
  if (models.length > 0 || stats.usage_total) {
    const rows = models.map(([name, u]) => usageRow(name, u));
    if (stats.usage_total) {
      rows.push(usageRow("total", stats.usage_total));
    }
    sections.push(
      "Tokens by model\n\n" +
        table(["Model", "Input", "Output", "Cache read", "Cache write", "Thinking", "Messages"], rows, [
          false,
          true,
          true,
          true,
          true,
          true,
          true,
        ])
    );
  }

  if (stats.top_files?.length) {
    sections.push(
      "Most edited files\n\n" +
        table(
          ["File", "Edits"],
          stats.top_files.map((f) => [f.path, formatNumber(f.edits)]),
          [false, true]
        )
    );
  }

  if (stats.days?.length) {
    sections.push(
      "By day\n\n" +
        table(
          ["Date", "Edits", "Sessions", "Files", "Added", "Removed"],
          stats.days.map((d) => [
            d.date,
            formatNumber(d.edits),
            formatNumber(d.sessions),
            formatNumber(d.files),
            formatNumber(d.lines_added),
            formatNumber(d.lines_removed),
          ]),
          [false, true, true, true, true, true]
        )
    );
  }

  return sections.join("\n\n") + "\n";
}
