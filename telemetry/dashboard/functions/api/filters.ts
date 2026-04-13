import {
  Env,
  errorResponse,
  HISTORICAL_REVIEW_MODEL,
  HISTORICAL_TRIAGE_MODEL,
  jsonResponse,
  MODEL_FAMILIES,
  num,
  querySQL,
  rangeDays,
  table,
} from "./_utils";

// GET /api/filters?range=7d|30d|90d
//
// Returns the set of filter values that actually appear in the data,
// so dropdowns don't list combinations that would return empty.
//
// Installations are capped to the top 30 by review count — full cardinality
// can be large and we only need the heaviest users for drill-down.

const TOP_INSTALLATIONS_PER_PLATFORM = 20;

// Platform values are interpolated raw into the SQL below — keep
// the type narrow so a future caller can't accidentally pass
// arbitrary input. The runtime guard catches any drift the type
// system misses (e.g. an `as Platform` cast at the call site).
type Platform = "github" | "local";

function topInstallsPerPlatform(env: Env, base: string, platform: Platform) {
  if (platform !== "github" && platform !== "local") {
    throw new Error(`invalid platform: ${platform}`);
  }

  // The dashboard auto-hides github installs with exactly one review
  // (treated as config-failure noise). Mirror that filter here so the
  // dropdown doesn't list installs whose data is hidden everywhere else.
  const noiseFilter =
    platform === "github" ? "HAVING SUM(_sample_interval) > 1" : "";

  return querySQL<{ key: string; count: number | string }>(
    env,
    `SELECT index1 AS key, SUM(_sample_interval) AS count
     ${base}
     AND blob6 = '${platform}'
     GROUP BY key
     ${noiseFilter}
     ORDER BY count DESC
     LIMIT ${TOP_INSTALLATIONS_PER_PLATFORM}`,
  ).then((rows) => rows.map((r) => ({ ...r, platform })));
}

export const onRequestGet: PagesFunction<Env> = async (ctx) => {
  try {
    const url = new URL(ctx.request.url);
    const days = rangeDays(url);

    const base = `
      FROM ${table(ctx.env)}
      WHERE blob1 = 'review_completed'
        AND timestamp > NOW() - INTERVAL '${days}' DAY
    `;

    const [
      providers,
      platforms,
      reviewModels,
      triageModels,
      githubInstalls,
      localInstalls,
    ] = await Promise.all([
        querySQL<{ key: string; count: number | string }>(
          ctx.env,
          `SELECT blob5 AS key, SUM(_sample_interval) AS count ${base}
           GROUP BY key ORDER BY count DESC`,
        ),
        querySQL<{ key: string; count: number | string }>(
          ctx.env,
          `SELECT blob6 AS key, SUM(_sample_interval) AS count ${base}
           GROUP BY key ORDER BY count DESC`,
        ),
        // Empty model values in historical rows are coalesced to match
        // the breakdown endpoint's display labels. See breakdown.ts.
        querySQL<{ key: string; count: number | string }>(
          ctx.env,
          `SELECT if(blob8 = '', '${HISTORICAL_REVIEW_MODEL}', blob8) AS key,
                  SUM(_sample_interval) AS count ${base}
           GROUP BY key ORDER BY count DESC`,
        ),
        querySQL<{ key: string; count: number | string }>(
          ctx.env,
          `SELECT if(blob9 = '', '${HISTORICAL_TRIAGE_MODEL}', blob9) AS key,
                  SUM(_sample_interval) AS count ${base}
           GROUP BY key ORDER BY count DESC`,
        ),
        // Top-N installations per platform. Two parallel queries so
        // a platform with lots of activity (typically GitHub) can't
        // shadow a less-active one (local) in a single global LIMIT.
        topInstallsPerPlatform(ctx.env, base, "github"),
        topInstallsPerPlatform(ctx.env, base, "local"),
      ]);

    const toBuckets = (rows: Array<{ key: string; count: number | string }>) =>
      rows
        .map((r) => ({ key: r.key || "", count: num(r.count) }))
        .filter((b) => b.key && b.count > 0);

    const reviewModelBuckets = toBuckets(reviewModels);
    const triageModelBuckets = toBuckets(triageModels);

    // Compute family roll-ups from the model buckets. A family entry
    // appears only if it actually matches something in the data, so
    // the dropdown never lists empty options.
    const computeFamilies = (
      buckets: Array<{ key: string; count: number }>,
      historicalSentinel: string,
      historicalDefault: string,
    ) => {
      return MODEL_FAMILIES.map((family) => {
        let count = 0;
        for (const b of buckets) {
          const key = b.key.toLowerCase();
          if (key.includes(family)) {
            count += b.count;
          } else if (
            family === historicalDefault &&
            b.key === historicalSentinel
          ) {
            count += b.count;
          }
        }
        return { key: family, count };
      }).filter((b) => b.count > 0);
    };

    return jsonResponse({
      range_days: days,
      providers: toBuckets(providers),
      platforms: toBuckets(platforms),
      review_models: reviewModelBuckets,
      triage_models: triageModelBuckets,
      review_model_families: computeFamilies(
        reviewModelBuckets,
        HISTORICAL_REVIEW_MODEL,
        "sonnet",
      ),
      triage_model_families: computeFamilies(
        triageModelBuckets,
        HISTORICAL_TRIAGE_MODEL,
        "haiku",
      ),
      installations: [...githubInstalls, ...localInstalls]
        .map((r) => ({
          key: r.key || "",
          platform: r.platform || "",
          count: num(r.count),
        }))
        .filter((b) => b.key && b.count > 0),
    });
  } catch (err) {
    const message = err instanceof Error ? err.message : "unknown error";
    return errorResponse(message, 500);
  }
};
