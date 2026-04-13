import {
  buildWhereWithDefaults,
  Env,
  Filters,
  handleError,
  HISTORICAL_REVIEW_MODEL,
  HISTORICAL_TRIAGE_MODEL,
  jsonResponse,
  num,
  parseFilters,
  querySQL,
  rangeDays,
  table,
} from "./_utils";

// GET /api/breakdown?dim=provider|platform|os|arch|version|model|severity|incremental
//   &range=7d|30d|90d[&filters...]
//
// When the breakdown dimension matches an active filter we drop that
// filter so the chart stays informative (otherwise a single bucket
// would fill the chart). E.g. dim=provider ignores the provider filter.

type Dim =
  | "provider"
  | "platform"
  | "os"
  | "arch"
  | "version"
  | "review_model"
  | "triage_model"
  | "severity"
  | "incremental";

// Model columns coalesce blank values (pre-change historical data) to
// sensible defaults for display. Analytics Engine is append-only — we
// can't backfill the empty rows, so we infer at query time. This only
// affects what the dashboard shows; the stored data is untouched, and
// all post-change rows carry the real model names from the client.
const BLOB_DIMS: Record<
  Exclude<Dim, "severity" | "incremental">,
  string
> = {
  provider: "blob5",
  platform: "blob6",
  os: "blob3",
  arch: "blob4",
  version: "blob2",
  review_model: `if(blob8 = '', '${HISTORICAL_REVIEW_MODEL}', blob8)`,
  triage_model: `if(blob9 = '', '${HISTORICAL_TRIAGE_MODEL}', blob9)`,
};

const DIM_TO_FILTER: Partial<Record<Dim, keyof Filters>> = {
  provider: "provider",
  platform: "platform",
  review_model: "review_model",
  triage_model: "triage_model",
};

function parseDim(raw: string | null): Dim {
  const dims: Dim[] = [
    "provider",
    "platform",
    "os",
    "arch",
    "version",
    "review_model",
    "triage_model",
    "severity",
    "incremental",
  ];
  return (dims.find((d) => d === raw) as Dim) ?? "provider";
}

interface Bucket {
  key: string;
  value: number;
}

async function blobBreakdown(
  env: Env,
  column: string,
  days: number,
  where: string,
): Promise<Bucket[]> {
  const sql = `
    SELECT ${column} AS key, SUM(_sample_interval) AS value
    FROM ${table(env)}
    WHERE blob1 = 'review_completed'
      AND timestamp > NOW() - INTERVAL '${days}' DAY
      ${where}
    GROUP BY key
    ORDER BY value DESC
  `;
  const rows = await querySQL<{ key: string; value: number | string }>(env, sql);
  return rows
    .map((r) => ({ key: r.key || "(empty)", value: num(r.value) }))
    .filter((b) => b.value > 0);
}

async function severityBreakdown(
  env: Env,
  days: number,
  where: string,
): Promise<Bucket[]> {
  const sql = `
    SELECT
      SUM(double9 * _sample_interval) AS critical,
      SUM(double10 * _sample_interval) AS bug,
      SUM(double11 * _sample_interval) AS warning,
      SUM(double12 * _sample_interval) AS suggestion,
      SUM(double13 * _sample_interval) AS nitpick
    FROM ${table(env)}
    WHERE blob1 = 'review_completed'
      AND timestamp > NOW() - INTERVAL '${days}' DAY
      ${where}
  `;
  const rows = await querySQL<Record<string, number | string>>(env, sql);
  const row = rows[0] ?? {};
  return (["critical", "bug", "warning", "suggestion", "nitpick"] as const)
    .map((k) => ({ key: k, value: num(row[k]) }))
    .filter((b) => b.value > 0);
}

async function incrementalBreakdown(
  env: Env,
  days: number,
  where: string,
): Promise<Bucket[]> {
  const sql = `
    SELECT double8 AS flag, SUM(_sample_interval) AS value
    FROM ${table(env)}
    WHERE blob1 = 'review_completed'
      AND timestamp > NOW() - INTERVAL '${days}' DAY
      ${where}
    GROUP BY flag
    ORDER BY value DESC
  `;
  const rows = await querySQL<{ flag: number | string; value: number | string }>(
    env,
    sql,
  );
  return rows
    .map((r) => ({
      key: num(r.flag) === 1 ? "incremental" : "full",
      value: num(r.value),
    }))
    .filter((b) => b.value > 0);
}

export const onRequestGet: PagesFunction<Env> = async (ctx) => {
  try {
    const url = new URL(ctx.request.url);
    const days = rangeDays(url);
    const dim = parseDim(url.searchParams.get("dim"));
    const filters = parseFilters(url);

    // Drop the filter that matches the current breakdown dimension.
    const exclude = DIM_TO_FILTER[dim] ? [DIM_TO_FILTER[dim]!] : [];
    const where = await buildWhereWithDefaults(ctx.env, filters, days, exclude);

    let buckets: Bucket[];
    if (dim === "severity") {
      buckets = await severityBreakdown(ctx.env, days, where);
    } else if (dim === "incremental") {
      buckets = await incrementalBreakdown(ctx.env, days, where);
    } else {
      buckets = await blobBreakdown(ctx.env, BLOB_DIMS[dim], days, where);
    }

    return jsonResponse({ dim, range_days: days, filters, buckets });
  } catch (err) {
    return handleError(err);
  }
};
