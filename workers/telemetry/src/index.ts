interface Env {
  TELEMETRY: AnalyticsEngineDataset;
}

const VALID_EVENTS = new Set(["review_completed", "setup_completed"]);

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    if (request.method === "OPTIONS") {
      return new Response(null, { status: 204 });
    }

    if (request.method !== "POST" || new URL(request.url).pathname !== "/telemetry") {
      return Response.json({ error: "Not found" }, { status: 404 });
    }

    try {
      const body = (await request.json()) as Record<string, unknown>;

      const event = body.event as string;
      if (!event || !VALID_EVENTS.has(event)) {
        return Response.json({ error: "Invalid event type" }, { status: 400 });
      }

      const installationId = body.installation_id as string;
      if (!installationId || installationId.length !== 36) {
        return Response.json(
          { error: "Invalid installation_id" },
          { status: 400 }
        );
      }

      const str = (key: string) => (body[key] as string) || "";
      const num = (key: string) => (body[key] as number) || 0;

      const bySeverity = (body.by_severity as Record<string, number>) || {};

      env.TELEMETRY.writeDataPoint({
        indexes: [installationId],
        blobs: [
          event, // blob1
          str("version"), // blob2
          str("os"), // blob3
          str("arch"), // blob4
          str("provider"), // blob5
          str("platform"), // blob6
          JSON.stringify(bySeverity), // blob7
        ],
        doubles: [
          num("new_findings"), // double1
          num("still_open_findings"), // double2
          num("input_tokens"), // double3
          num("output_tokens"), // double4
          num("cache_read_tokens"), // double5
          num("cost_usd"), // double6
          num("duration_ms"), // double7
          body.is_incremental ? 1 : 0, // double8
          bySeverity["critical"] || 0, // double9
          bySeverity["bug"] || 0, // double10
          bySeverity["warning"] || 0, // double11
          bySeverity["suggestion"] || 0, // double12
          bySeverity["nitpick"] || 0, // double13
        ],
      });

      return Response.json({ ok: true });
    } catch {
      return Response.json({ error: "Bad request" }, { status: 400 });
    }
  },
};
