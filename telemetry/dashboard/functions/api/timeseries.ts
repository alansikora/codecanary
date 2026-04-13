import {
  buildWhereWithDefaults,
  Env,
  errorResponse,
  jsonResponse,
  num,
  parseFilters,
  querySQL,
  rangeDays,
  table,
} from "./_utils";

// GET /api/timeseries?metric=reviews|cost|duration|findings|installations
//   &range=7d|30d|90d[&provider=...&platform=...&model=...&installation=...]

type Metric = "reviews" | "cost" | "duration" | "findings" | "installations";

const METRIC_EXPR: Record<Metric, string> = {
  reviews: "SUM(_sample_interval)",
  cost: "SUM(double6 * _sample_interval)",
  // Every GROUP BY day row has at least one event, so divisor is non-zero.
  duration: "SUM(double7 * _sample_interval) / SUM(_sample_interval)",
  findings: "SUM((double1 + double2) * _sample_interval)",
  installations: "count(DISTINCT index1)",
};

function parseMetric(raw: string | null): Metric {
  if (raw && raw in METRIC_EXPR) return raw as Metric;
  return "reviews";
}

export const onRequestGet: PagesFunction<Env> = async (ctx) => {
  try {
    const url = new URL(ctx.request.url);
    const days = rangeDays(url);
    const metric = parseMetric(url.searchParams.get("metric"));
    const filters = parseFilters(url);

    const where = await buildWhereWithDefaults(ctx.env, filters, days);

    const sql = `
      SELECT
        toStartOfDay(timestamp) AS day,
        ${METRIC_EXPR[metric]} AS value
      FROM ${table(ctx.env)}
      WHERE blob1 = 'review_completed'
        AND timestamp > NOW() - INTERVAL '${days}' DAY
        ${where}
      GROUP BY day
      ORDER BY day ASC
    `;

    const rows = await querySQL<{ day: string; value: number | string }>(
      ctx.env,
      sql,
    );

    const points = rows.map((r) => ({
      date: typeof r.day === "string" ? r.day : String(r.day),
      value: num(r.value),
    }));

    return jsonResponse({ metric, range_days: days, filters, points });
  } catch (err) {
    const message = err instanceof Error ? err.message : "unknown error";
    return errorResponse(message, 500);
  }
};
