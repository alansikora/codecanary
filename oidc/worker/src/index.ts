import * as jose from "jose";

interface Env {
  APP_ID: string;
  APP_PRIVATE_KEY: string;
}

const GITHUB_OIDC_ISSUER = "https://token.actions.githubusercontent.com";
const GITHUB_API = "https://api.github.com";

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const corsHeaders = {
      "Access-Control-Allow-Origin": "*",
      "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
      "Access-Control-Allow-Headers": "Authorization, Content-Type",
    };

    if (request.method === "OPTIONS") {
      return new Response(null, { status: 204, headers: corsHeaders });
    }

    const withCors = (resp: Response): Response => {
      const patched = new Response(resp.body, resp);
      for (const [k, v] of Object.entries(corsHeaders)) {
        patched.headers.set(k, v);
      }
      return patched;
    };

    const url = new URL(request.url);

    // GET /check-install?repo=owner/name — lightweight installation check.
    if (request.method === "GET" && url.pathname === "/check-install") {
      return withCors(await handleCheckInstall(request, url, env));
    }

    if (request.method !== "POST" || url.pathname !== "/token") {
      return withCors(Response.json({ error: "Not found" }, { status: 404 }));
    }

    try {
      // 1. Extract OIDC token from Authorization header.
      const authHeader = request.headers.get("Authorization");
      if (!authHeader?.startsWith("Bearer ")) {
        return Response.json({ error: "Missing Authorization header" }, { status: 401 });
      }
      const oidcToken = authHeader.slice(7);

      // 1b. Read the requested mode. A malformed body is rejected before
      // anything is minted: it must never fall through to a full token.
      const parsed = await parseTokenRequest(request);
      if ("error" in parsed) {
        return Response.json({ error: parsed.error }, { status: 400 });
      }
      const permissions = tokenPermissions(parsed.mode);

      // 2. Verify OIDC token against GitHub's JWKS.
      const JWKS = jose.createRemoteJWKSet(
        new URL(`${GITHUB_OIDC_ISSUER}/.well-known/jwks`)
      );

      const { payload } = await jose.jwtVerify(oidcToken, JWKS, {
        issuer: GITHUB_OIDC_ISSUER,
        audience: "codecanary",
      });

      // 3. Extract repository from OIDC claims.
      const repository = payload.repository as string | undefined;
      if (!repository || repository.split("/").length !== 2) {
        return Response.json({ error: "Invalid or missing repository claim in OIDC token" }, { status: 400 });
      }

      // 4. Generate GitHub App JWT.
      const appJwt = await generateAppJwt(env.APP_ID, env.APP_PRIVATE_KEY);

      // 5. Find the installation for this repository.
      const installationId = await findInstallation(appJwt, repository);
      if (!installationId) {
        return Response.json(
          { error: `CodeCanary Review app is not installed on ${repository}` },
          { status: 404 }
        );
      }

      // 6. Generate an installation token scoped to the requesting repo,
      // with the permissions the mode asks for (see tokenPermissions).
      const repo = repository.split("/")[1];
      const tokenRequest: { repositories: string[]; permissions?: Record<string, string> } = {
        repositories: [repo],
      };
      if (permissions) {
        tokenRequest.permissions = permissions;
      }
      const tokenResponse = await fetch(
        `${GITHUB_API}/app/installations/${installationId}/access_tokens`,
        {
          method: "POST",
          headers: {
            Authorization: `Bearer ${appJwt}`,
            Accept: "application/vnd.github+json",
            "User-Agent": "codecanary-token-proxy",
          },
          body: JSON.stringify(tokenRequest),
        }
      );

      if (!tokenResponse.ok) {
        const body = await tokenResponse.text();
        console.error(`GitHub API error (status ${tokenResponse.status}): ${body}`);
        return Response.json(
          { error: "Failed to create installation token" },
          { status: 502 }
        );
      }

      // GitHub echoes what it granted; pass that on rather than what was
      // asked, so the caller checks the token it actually holds.
      const tokenData = (await tokenResponse.json()) as {
        token: string;
        permissions?: Record<string, string>;
      };
      return Response.json({ token: tokenData.token, permissions: tokenData.permissions ?? {} });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Unknown error";
      return Response.json({ error: message }, { status: 500 });
    }
  },
};

// REVIEW_PERMISSIONS is what `codecanary review` (the action's review and
// reply_only runs) needs, and nothing that writes code. Each entry, with the
// calls that need it:
//   contents: read       git fetch of the base or a previous commit over HTTPS
//                        with this token (ensureCommit, github_rules.go)
//   pull_requests: write gh pr view / gh pr diff, the PR's reviews, review
//                        comments and threads (read, post, edit the review
//                        body, reply, minimize), gh pr comment
//   statuses: write      the CodeCanary / review commit status
//                        (POST repos/{o}/{r}/statuses/{sha})
// metadata: read is always included by GitHub. The review reads no check
// runs, issues or Actions data with this token. It cannot resolve a review
// thread: see RESOLVE_PERMISSIONS.
export const REVIEW_PERMISSIONS: Readonly<Record<string, string>> = Object.freeze({
  contents: "read",
  pull_requests: "write",
  statuses: "write",
});

// RESOLVE_PERMISSIONS is what `codecanary resolve` needs: the action step
// that, after a review, resolves the threads the review settled. GitHub
// refuses the resolveReviewThread mutation to an installation token without
// contents: write (pull_requests: write alone gets "Resource not accessible
// by integration"), which is why the review cannot do it with its own token.
// This token is minted after the model has finished and goes only to that
// step, which takes nothing from the review but thread IDs.
//   contents: write      resolveReviewThread
//   pull_requests: write reading the PR's review threads, resolveReviewThread
export const RESOLVE_PERMISSIONS: Readonly<Record<string, string>> = Object.freeze({
  contents: "write",
  pull_requests: "write",
});

export type TokenMode = "review" | "resolve" | "fix" | "legacy";

// parseTokenRequest reads the optional body of POST /token:
// { "mode": "review" | "resolve" | "fix" }. No body, or a body without `mode`, is
// "legacy" and gets the App's full installation permissions, as before the
// body existed: action versions already pinned by users (v0.8.0 and older)
// send no body and must keep working. Any other mode is an error rather than
// a fall-back to the full token, so a typo ("Review") or a mode this worker
// predates fails loudly instead of quietly minting a token that can push.
// Anything that isn't a JSON object with an optional string `mode` is an
// error too.
export async function parseTokenRequest(
  request: Request
): Promise<{ mode: TokenMode } | { error: string }> {
  const text = (await request.text()).trim();
  if (text === "") {
    return { mode: "legacy" };
  }
  let body: unknown;
  try {
    body = JSON.parse(text);
  } catch {
    return { error: "Request body is not valid JSON" };
  }
  if (body === null || typeof body !== "object" || Array.isArray(body)) {
    return { error: 'Request body must be a JSON object like {"mode":"review"}' };
  }
  const mode = (body as { mode?: unknown }).mode;
  if (mode === undefined) {
    return { mode: "legacy" };
  }
  if (typeof mode !== "string") {
    return { error: '"mode" must be a string ("review", "resolve" or "fix")' };
  }
  if (mode === "review" || mode === "resolve" || mode === "fix") {
    return { mode };
  }
  return { error: `Unknown mode ${JSON.stringify(mode)}: expected "review", "resolve" or "fix"` };
}

// tokenPermissions is the `permissions` to request for mode, or undefined to
// inherit the installation's (fix must push; legacy is today's behaviour).
export function tokenPermissions(mode: TokenMode): Record<string, string> | undefined {
  if (mode === "review") {
    return { ...REVIEW_PERMISSIONS };
  }
  if (mode === "resolve") {
    return { ...RESOLVE_PERMISSIONS };
  }
  return undefined;
}

async function handleCheckInstall(request: Request, url: URL, env: Env): Promise<Response> {
  const authHeader = request.headers.get("Authorization");
  if (!authHeader?.startsWith("Bearer ")) {
    return Response.json({ error: "Missing Authorization header" }, { status: 401 });
  }
  const ghToken = authHeader.slice(7);

  const repo = url.searchParams.get("repo");
  if (!repo || repo.split("/").length !== 2) {
    return Response.json(
      { error: "Missing or invalid 'repo' parameter (expected owner/name)" },
      { status: 400 }
    );
  }

  // Verify the caller has access to this repo using their own token.
  // This prevents probing installation status for repos the user can't see.
  const repoResp = await fetch(`${GITHUB_API}/repos/${repo}`, {
    headers: {
      Authorization: `Bearer ${ghToken}`,
      Accept: "application/vnd.github+json",
      "User-Agent": "codecanary-token-proxy",
    },
  });
  if (repoResp.status === 401) {
    await repoResp.text();
    return Response.json({ error: "Invalid GitHub token" }, { status: 401 });
  }
  if (!repoResp.ok) {
    await repoResp.text();
    return Response.json({ error: "Repository not accessible" }, { status: 403 });
  }

  try {
    const appJwt = await generateAppJwt(env.APP_ID, env.APP_PRIVATE_KEY);
    const installationId = await findInstallation(appJwt, repo);
    return Response.json({ installed: installationId !== null });
  } catch (err) {
    const message = err instanceof Error ? err.message : "Unknown error";
    return Response.json({ error: message }, { status: 500 });
  }
}

async function generateAppJwt(appId: string, privateKeyPem: string): Promise<string> {
  if (!privateKeyPem.includes("BEGIN PRIVATE KEY")) {
    throw new Error(
      'APP_PRIVATE_KEY must be in PKCS#8 format ("BEGIN PRIVATE KEY"). Convert with: openssl pkcs8 -topk8 -inform PEM -outform PEM -nocrypt -in key.pem'
    );
  }
  const privateKey = await jose.importPKCS8(privateKeyPem, "RS256");
  const now = Math.floor(Date.now() / 1000);

  return new jose.SignJWT({})
    .setProtectedHeader({ alg: "RS256" })
    .setIssuer(appId)
    .setIssuedAt(now - 60)
    .setExpirationTime(now + 600)
    .sign(privateKey);
}

async function findInstallation(
  appJwt: string,
  repository: string
): Promise<number | null> {
  const response = await fetch(
    `${GITHUB_API}/repos/${repository}/installation`,
    {
      headers: {
        Authorization: `Bearer ${appJwt}`,
        Accept: "application/vnd.github+json",
        "User-Agent": "codecanary-token-proxy",
      },
    }
  );

  if (response.ok) {
    const data = (await response.json()) as { id: number };
    return data.id;
  }

  return null;
}
