import * as jose from "jose";

interface Env {
  APP_ID: string;
  APP_PRIVATE_KEY: string;
}

const GITHUB_OIDC_ISSUER = "https://token.actions.githubusercontent.com";
const GITHUB_API = "https://api.github.com";

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    if (request.method === "OPTIONS") {
      return new Response(null, { status: 204 });
    }

    const url = new URL(request.url);

    // GET /check-install?repo=owner/name — lightweight installation check.
    if (request.method === "GET" && url.pathname === "/check-install") {
      return handleCheckInstall(url, env);
    }

    if (request.method !== "POST" || url.pathname !== "/token") {
      return Response.json({ error: "Not found" }, { status: 404 });
    }

    try {
      // 1. Extract OIDC token from Authorization header.
      const authHeader = request.headers.get("Authorization");
      if (!authHeader?.startsWith("Bearer ")) {
        return Response.json({ error: "Missing Authorization header" }, { status: 401 });
      }
      const oidcToken = authHeader.slice(7);

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

      // 6. Generate an installation token scoped to the requesting repo.
      // Permissions are inherited from the App's installation config.
      const repo = repository.split("/")[1];
      const tokenResponse = await fetch(
        `${GITHUB_API}/app/installations/${installationId}/access_tokens`,
        {
          method: "POST",
          headers: {
            Authorization: `Bearer ${appJwt}`,
            Accept: "application/vnd.github+json",
            "User-Agent": "codecanary-token-proxy",
          },
          body: JSON.stringify({
            repositories: [repo],
          }),
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

      const tokenData = (await tokenResponse.json()) as { token: string };
      return Response.json({ token: tokenData.token });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Unknown error";
      return Response.json({ error: message }, { status: 500 });
    }
  },
};

async function handleCheckInstall(url: URL, env: Env): Promise<Response> {
  const repo = url.searchParams.get("repo");
  if (!repo || repo.split("/").length !== 2) {
    return Response.json(
      { error: "Missing or invalid 'repo' parameter (expected owner/name)" },
      { status: 400 }
    );
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
