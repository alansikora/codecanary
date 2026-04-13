import {
  buildWhereWith,
  Env,
  errorResponse,
  jsonResponse,
  num,
  parseFilters,
  querySQL,
  rangeDays,
  resolveExcludedIds,
  table,
} from "./_utils";

// GET /api/overview?range=7d|30d|90d[&provider=...&platform=...&model=...&installation=...]
//
// Returns aggregate card stats for the requested window and filters.
// Also returns a per-platform installation split. The split ignores
// the `platform` filter (so it stays informative even when a platform
// is selected — otherwise one side would always be zero).
export const onRequestGet: PagesFunction<Env> = async (ctx) => {
  try {
    const url = new URL(ctx.request.url);
    const days = rangeDays(url);
    const filters = parseFilters(url);

    // Fetch the auto-exclusion list once and share it across both
    // WHERE variants below — they're derived from the same data, so
    // re-querying would just double the AE round trips.
    const excludedIds = await resolveExcludedIds(ctx.env, days);

    // The platform-split query mirrors `where` but drops the platform
    // filter, so both GitHub and local buckets stay visible even when
    // a single platform is selected.
    const where = buildWhereWith(filters, excludedIds);
    const wherePlatformAgnostic = buildWhereWith(filters, excludedIds, [
      "platform",
    ]);

    // Main aggregate query (honors all filters).
    const mainSQL = `
      SELECT
        SUM(_sample_interval) AS reviews,
        count(DISTINCT index1) AS installations,
        SUM(double6 * _sample_interval) AS cost_usd,
        SUM((double1 + double2) * _sample_interval) AS findings,
        SUM(double7 * _sample_interval) AS duration_ms_total,
        SUM(double3 * _sample_interval) AS input_tokens,
        SUM(double4 * _sample_interval) AS output_tokens,
        SUM(double5 * _sample_interval) AS cache_read_tokens
      FROM ${table(ctx.env)}
      WHERE blob1 = 'review_completed'
        AND timestamp > NOW() - INTERVAL '${days}' DAY
        ${where}
    `;

    // Platform split (ignores platform filter so both buckets are visible).
    const splitSQL = `
      SELECT blob6 AS platform, count(DISTINCT index1) AS installations
      FROM ${table(ctx.env)}
      WHERE blob1 = 'review_completed'
        AND timestamp > NOW() - INTERVAL '${days}' DAY
        ${wherePlatformAgnostic}
      GROUP BY platform
    `;

    const [mainRows, splitRows] = await Promise.all([
      querySQL(ctx.env, mainSQL),
      querySQL<{ platform: string; installations: number | string }>(
        ctx.env,
        splitSQL,
      ),
    ]);

    const row = mainRows[0] ?? {};
    const reviews = num(row["reviews"]);
    const byPlatform: Record<string, number> = {};
    for (const r of splitRows) {
      if (r.platform) byPlatform[r.platform] = num(r.installations);
    }

    return jsonResponse({
      range_days: days,
      filters,
      reviews,
      installations: {
        total: num(row["installations"]),
        by_platform: byPlatform,
      },
      cost_usd: num(row["cost_usd"]),
      findings: num(row["findings"]),
      avg_cost_usd: reviews > 0 ? num(row["cost_usd"]) / reviews : 0,
      avg_duration_ms:
        reviews > 0 ? num(row["duration_ms_total"]) / reviews : 0,
      tokens: {
        input: num(row["input_tokens"]),
        output: num(row["output_tokens"]),
        cache_read: num(row["cache_read_tokens"]),
      },
    });
  } catch (err) {
    const message = err instanceof Error ? err.message : "unknown error";
    return errorResponse(message, 500);
  }
};
