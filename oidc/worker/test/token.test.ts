import { generateKeyPairSync } from "node:crypto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// OIDC verification is stubbed: these tests are about what the worker asks
// GitHub for, not about JWKS. The App JWT is signed for real.
vi.mock("jose", async (importOriginal) => {
  const actual = await importOriginal<typeof import("jose")>();
  return {
    ...actual,
    createRemoteJWKSet: vi.fn(() => ({})),
    jwtVerify: vi.fn(async () => ({ payload: { repository: "acme/widget" } })),
  };
});

import worker, { REVIEW_PERMISSIONS } from "../src/index";

const { privateKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
const env = {
  APP_ID: "12345",
  APP_PRIVATE_KEY: privateKey.export({ type: "pkcs8", format: "pem" }).toString(),
};

// The App's own permissions: what GitHub grants when none are requested.
const APP_PERMISSIONS = {
  contents: "write",
  pull_requests: "write",
  statuses: "write",
  metadata: "read",
};

let tokenRequests: Array<Record<string, unknown>>;

beforeEach(() => {
  tokenRequests = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const url = String(input);
      if (url === "https://api.github.com/repos/acme/widget/installation") {
        return Response.json({ id: 42 });
      }
      if (url === "https://api.github.com/app/installations/42/access_tokens") {
        expect(init?.method).toBe("POST");
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        tokenRequests.push(body);
        const granted = (body.permissions as Record<string, string> | undefined) ?? APP_PERMISSIONS;
        return Response.json({ token: "ghs_test", permissions: { ...granted, metadata: "read" } }, { status: 201 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    })
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function tokenRequest(body?: string): Request {
  return new Request("https://oidc.codecanary.sh/token", {
    method: "POST",
    headers: { Authorization: "Bearer oidc-token", "Content-Type": "application/json" },
    body,
  });
}

describe("POST /token", () => {
  it("review mode asks GitHub for the reduced permissions and returns what was granted", async () => {
    const resp = await worker.fetch(tokenRequest('{"mode":"review"}'), env);
    expect(resp.status).toBe(200);
    expect(tokenRequests).toEqual([
      {
        repositories: ["widget"],
        permissions: { contents: "read", pull_requests: "write", statuses: "write" },
      },
    ]);
    const data = (await resp.json()) as { token: string; permissions: Record<string, string> };
    expect(data.token).toBe("ghs_test");
    expect(data.permissions.contents).toBe("read");
  });

  it("the review set never writes contents", () => {
    expect(REVIEW_PERMISSIONS.contents).toBe("read");
  });

  it.each([
    ["fix mode", '{"mode":"fix"}'],
    ["no body", undefined],
    ["empty body", ""],
    ["an object without mode", "{}"],
  ])("%s requests no permissions, so the installation's apply", async (_name, body) => {
    const resp = await worker.fetch(tokenRequest(body), env);
    expect(resp.status).toBe(200);
    expect(tokenRequests).toEqual([{ repositories: ["widget"] }]);
    const data = (await resp.json()) as { permissions: Record<string, string> };
    expect(data.permissions.contents).toBe("write");
  });

  it.each([
    ["invalid JSON", "{mode: review"],
    ["a JSON array", '["review"]'],
    ["a JSON string", '"review"'],
    ["null", "null"],
    ["a non-string mode", '{"mode":1}'],
    ["an unknown mode", '{"mode":"deploy"}'],
    ["a miscased mode", '{"mode":"Review"}'],
    ["an empty mode", '{"mode":""}'],
  ])("%s is a 400 and mints nothing", async (_name, body) => {
    const resp = await worker.fetch(tokenRequest(body), env);
    expect(resp.status).toBe(400);
    const data = (await resp.json()) as { error: string; token?: string };
    expect(data.error).toBeTruthy();
    expect(data.token).toBeUndefined();
    expect(tokenRequests).toEqual([]);
  });
});
