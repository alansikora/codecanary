// Shared helpers for the dashboard's read-only Analytics Engine queries.
//
// The dashboard does NOT bind to Analytics Engine directly. Writes happen
// exclusively through the ingestion Worker's binding (workers/telemetry).
// Reads go through the SQL HTTP API with a token scoped to
// "Account Analytics: Read" — platform-enforced read-only.

export interface Env {
  CF_ACCOUNT_ID: string;
  CF_API_TOKEN: string;
  AE_DATASET: string;
}

interface AEResponse {
  meta?: Array<{ name: string; type: string }>;
  data?: Array<Record<string, unknown>>;
  rows?: number;
}

export async function querySQL<T = Record<string, unknown>>(
  env: Env,
  sql: string,
): Promise<T[]> {
  if (!env.CF_ACCOUNT_ID || !env.CF_API_TOKEN) {
    throw new Error("CF_ACCOUNT_ID and CF_API_TOKEN must be configured");
  }

  const url = `https://api.cloudflare.com/client/v4/accounts/${env.CF_ACCOUNT_ID}/analytics_engine/sql`;
  const res = await fetch(url, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${env.CF_API_TOKEN}`,
      "Content-Type": "text/plain;charset=UTF-8",
    },
    body: sql,
  });

  if (!res.ok) {
    const text = await res.text();
    throw new Error(`AE query failed (${res.status}): ${text}`);
  }

  const body = (await res.json()) as AEResponse;
  return (body.data ?? []) as T[];
}

export function rangeDays(url: URL, fallback = 30): number {
  const raw = url.searchParams.get("range");
  switch (raw) {
    case "7d":
      return 7;
    case "30d":
      return 30;
    case "90d":
      return 90;
    default:
      return fallback;
  }
}

export function table(env: Env): string {
  return `'${env.AE_DATASET || "codecanary_telemetry"}'`;
}

export function jsonResponse(data: unknown, status = 200): Response {
  return Response.json(data, {
    status,
    headers: {
      "Cache-Control": "private, max-age=60",
      "Content-Type": "application/json; charset=UTF-8",
    },
  });
}

export function errorResponse(message: string, status = 500): Response {
  return Response.json({ error: message }, { status });
}

export function num(v: unknown): number {
  if (typeof v === "number") return v;
  if (typeof v === "string") {
    const n = Number(v);
    return Number.isFinite(n) ? n : 0;
  }
  return 0;
}

// ---------- Filters ----------
//
// Filters are passed as URL query params. Every value is sanitized
// before interpolation: installation_id must be a 36-char UUID, and
// the free-form string filters (provider / platform / model) only
// allow a conservative charset. Anything else is silently dropped,
// which is safer than 400-ing on odd inputs.

export interface Filters {
  provider?: string;
  platform?: string;
  review_model?: string;
  triage_model?: string;
  installation?: string;
}

// Allow spaces and parens so historical model sentinels like
// "sonnet (historical)" survive sanitization. No SQL-terminating
// characters (quote, semicolon, backslash) are permitted.
const SAFE_STRING = /^[A-Za-z0-9 ._:/@()-]{1,100}$/;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

// Sentinels used by the breakdown/filters endpoints to label rows
// whose model columns predate the telemetry addition. Exported so
// buildWhere can translate them to the actual stored value (empty).
export const HISTORICAL_REVIEW_MODEL = "sonnet (historical)";
export const HISTORICAL_TRIAGE_MODEL = "haiku (historical)";

// Known model families for the "catch-all" filter. A selection like
// "family:haiku" matches any model whose name contains "haiku" (case
// insensitive). When the family matches the historical default for
// that column, empty-string rows are included too.
export const MODEL_FAMILIES = [
  "haiku",
  "sonnet",
  "opus",
  "gpt-5",
  "gpt-4",
  "o1",
  "o3",
  "o4",
];

const FAMILY_PREFIX = "family:";

// modelClause renders a WHERE clause fragment for a model filter.
//   - "family:X"                    → case-insensitive contains match,
//                                     plus empty if X is the historical default
//   - HISTORICAL_REVIEW/TRIAGE_MODEL → blob = ''
//   - anything else                  → blob = 'value' (exact)
function modelClause(
  column: string,
  raw: string,
  historicalDefault: string,
): string {
  const historicalSentinel =
    column === "blob8" ? HISTORICAL_REVIEW_MODEL : HISTORICAL_TRIAGE_MODEL;

  if (raw.startsWith(FAMILY_PREFIX)) {
    const stem = raw.slice(FAMILY_PREFIX.length).toLowerCase();
    let clause = `lower(${column}) LIKE '%${stem}%'`;
    if (stem === historicalDefault) {
      clause = `(${clause} OR ${column} = '')`;
    }
    return clause;
  }

  if (raw === historicalSentinel) {
    return `${column} = ''`;
  }

  return `${column} = '${raw}'`;
}

function sanitizeString(raw: string | null): string | undefined {
  if (!raw) return undefined;
  return SAFE_STRING.test(raw) ? raw : undefined;
}

function sanitizeUUID(raw: string | null): string | undefined {
  if (!raw) return undefined;
  return UUID.test(raw) ? raw : undefined;
}

export function parseFilters(url: URL): Filters {
  return {
    provider: sanitizeString(url.searchParams.get("provider")),
    platform: sanitizeString(url.searchParams.get("platform")),
    review_model: sanitizeString(url.searchParams.get("review_model")),
    triage_model: sanitizeString(url.searchParams.get("triage_model")),
    installation: sanitizeUUID(url.searchParams.get("installation")),
  };
}

// buildWhereWithDefaults composes the user filter clauses with the
// dashboard's automatic exclusions. Currently the only exclusion is
// "GitHub installations with exactly one review in the window" — these
// are almost always config failures and would otherwise inflate the
// install count and skew the platform mix.
//
// AE doesn't support subqueries, so the excluded IDs are pre-fetched
// and folded in as a NOT IN list.
export async function buildWhereWithDefaults(
  env: Env,
  filters: Filters,
  days: number,
  exclude: Array<keyof Filters> = [],
): Promise<string> {
  let where = buildWhere(filters, exclude);
  const ids = await resolveExcludedIds(env, days);
  if (ids.length > 0) {
    where += ` AND index1 NOT IN (${ids.map((id) => `'${id}'`).join(",")})`;
  }
  return where;
}

// resolveExcludedIds returns installation IDs that the dashboard
// always hides: github installs that ran exactly one review in the
// window. Capped at 5000 to keep the resulting NOT IN list bounded.
async function resolveExcludedIds(
  env: Env,
  days: number,
): Promise<string[]> {
  const rows = await querySQL<{ id: string }>(
    env,
    `SELECT index1 AS id
     FROM ${table(env)}
     WHERE blob1 = 'review_completed'
       AND timestamp > NOW() - INTERVAL '${days}' DAY
       AND blob6 = 'github'
     GROUP BY id
     HAVING SUM(_sample_interval) = 1
     LIMIT 5000`,
  );
  return rows.map((r) => r.id).filter((id) => UUID.test(id));
}

// buildWhere returns the additional WHERE clauses for the given filters,
// joined by AND. The leading " AND " is included so the caller can append
// directly to a base WHERE. Returns "" if no filters are set.
export function buildWhere(
  filters: Filters,
  exclude: Array<keyof Filters> = [],
): string {
  const clauses: string[] = [];
  const skip = new Set(exclude);
  if (filters.provider && !skip.has("provider")) {
    clauses.push(`blob5 = '${filters.provider}'`);
  }
  if (filters.platform && !skip.has("platform")) {
    clauses.push(`blob6 = '${filters.platform}'`);
  }
  if (filters.review_model && !skip.has("review_model")) {
    clauses.push(modelClause("blob8", filters.review_model, "sonnet"));
  }
  if (filters.triage_model && !skip.has("triage_model")) {
    clauses.push(modelClause("blob9", filters.triage_model, "haiku"));
  }
  if (filters.installation && !skip.has("installation")) {
    clauses.push(`index1 = '${filters.installation}'`);
  }
  return clauses.length ? " AND " + clauses.join(" AND ") : "";
}
