// E2E mock API — yalnızca geliştirme/E2E amaçlı; gerçek backend CI'da
// E2E_REAL=1 ile çalışır. Sözleşme (JSON şekilleri) gerçek API ile aynıdır:
// docs/architecture/api-and-protocol.md ve backend handler'ları eşleşir.
// CSRF: gerçek sunucuda olduğu gibi oturum token'ından türetilir.
import { createServer } from "node:http";
import { createHash, randomBytes } from "node:crypto";

const PORT = Number(process.env.MOCK_PORT ?? 8787);

const CSRF_PREFIX = "srvctl-csrf:v1:";
const deriveCsrf = (token) => createHash("sha256").update(CSRF_PREFIX + token).digest("hex");

// ---- In-memory durum ----
const users = new Map(); // username -> {id, password, display_name, status, created_at, last_login_at}
const sessions = new Map(); // token -> {user, created}
const nodes = [];
const groups = [];
const tasks = [];
const audit = [];
const enrollmentTokens = [];
let idSeq = 1;
const uuid = () =>
  `${String(idSeq++).padStart(8, "0")}-0000-4000-8000-${String(idSeq).padStart(12, "0")}`;

users.set("admin", {
  id: uuid(),
  password: "guclu-parola-12345",
  display_name: "E2E Yönetici",
  status: "active",
  created_at: new Date().toISOString(),
  last_login_at: null,
});

function logAudit(actor, action, targetType, targetId, result) {
  audit.unshift({
    id: audit.length + 1,
    actor,
    action,
    target_type: targetType,
    target_id: targetId,
    result,
    correlation_id: randomBytes(8).toString("hex"),
    metadata: {},
    created_at: new Date().toISOString(),
  });
}

const OPERATIONS = [
  { name: "noop.echo", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 30 },
  { name: "inventory.node.tag.set", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 30 },
];

function json(res, status, body) {
  const data = JSON.stringify(body);
  res.writeHead(status, { "Content-Type": "application/json; charset=utf-8" });
  res.end(data);
}

function errorBody(code, message, status) {
  return { code, message, correlation_id: "mock", retryable: false, status };
}

// "Worker": queued görevleri 400ms sonra succeeded yapar (gerçek worker
// davranışının E2E gözlenebilir simülasyonu).
setInterval(() => {
  console.log("[mock-tick]", tasks.map((t) => `${t.operation}:${t.status}`).join(",") || "bos");
  const now = new Date().toISOString();
  for (const t of tasks) {
    if (t.status === "queued") {
      t.status = "dispatched";
    } else if (t.status === "dispatched") {
      t.status = "succeeded";
      t.result = { ok: true };
      logAudit("system", "task.complete", "task", t.id, "success");
    }
  }
  void now;
}, 400).unref();

const server = createServer(async (req, res) => {
  const url = new URL(req.url, `http://127.0.0.1:${PORT}`);
  const path = url.pathname;
  const method = req.method ?? "GET";

  const readBody = () =>
    new Promise((resolve) => {
      let raw = "";
      req.on("data", (c) => (raw += c));
      req.on("end", () => {
        try {
          resolve(raw ? JSON.parse(raw) : {});
        } catch {
          resolve({});
        }
      });
    });

  const cookieToken = (() => {
    const m = /srvctl_session=([^;]+)/.exec(req.headers.cookie ?? "");
    return m ? decodeURIComponent(m[1]) : "";
  })();
  const session = sessions.get(cookieToken);
  const actor = session?.user?.username ?? "anonymous";

  const mutations = new Set(["POST", "PUT", "PATCH", "DELETE"]);
  if (mutations.has(method)) {
    const exempt =
      path === "/api/v1/auth/login" || path === "/api/v1/bootstrap/admin";
    const presented = req.headers["x-csrf-token"] ?? "";
    if (!exempt && (!session || presented !== deriveCsrf(cookieToken))) {
      json(res, 403, errorBody("csrf_invalid", "CSRF doğrulaması başarısız.", 403));
      return;
    }
  }

  // ---- auth ----
  if (method === "POST" && path === "/api/v1/auth/login") {
    const body = await readBody();
    const u = users.get(String(body.username ?? ""));
    if (!u || u.password !== body.password || u.status !== "active") {
      json(res, 401, errorBody("invalid_credentials", "Kullanıcı adı veya parola hatalı.", 401));
      return;
    }
    const token = randomBytes(32).toString("base64url");
    u.last_login_at = new Date().toISOString();
    sessions.set(token, { user: { id: u.id, username: "admin" }, created: Date.now() });
    logAudit("admin", "auth.login", "session", u.id, "success");
    res.setHeader(
      "Set-Cookie",
      `srvctl_session=${encodeURIComponent(token)}; Path=/; HttpOnly; SameSite=Strict`,
    );
    json(res, 200, {
      user: { id: u.id, username: "admin" },
      csrf_token: deriveCsrf(token),
      expires_at: new Date(Date.now() + 12 * 3600_000).toISOString(),
    });
    return;
  }
  if (method === "GET" && path === "/api/v1/auth/session") {
    if (!session) {
      json(res, 401, errorBody("unauthorized", "Oturum gerekli.", 401));
      return;
    }
    json(res, 200, {
      user_id: session.user.id,
      username: session.user.username,
      session_id: uuid(),
      server_time: new Date().toISOString(),
    });
    return;
  }
  if (method === "GET" && path === "/api/v1/auth/csrf") {
    if (!session) {
      json(res, 401, errorBody("unauthorized", "Oturum gerekli.", 401));
      return;
    }
    json(res, 200, { csrf_token: deriveCsrf(cookieToken) });
    return;
  }
  if (method === "POST" && path === "/api/v1/auth/logout") {
    sessions.delete(cookieToken);
    res.setHeader("Set-Cookie", "srvctl_session=; Path=/; Max-Age=0");
    json(res, 200, { status: "ok" });
    return;
  }

  // E2E health (webServer readiness için; oturumsuz).
  if (method === "GET" && path === "/e2e/health") {
    json(res, 200, { status: "ok", mock: true });
    return;
  }

  // Korunan uçlar: oturum şart.
  if (!session && path.startsWith("/api/v1/")) {
    json(res, 401, errorBody("unauthorized", "Oturum gerekli.", 401));
    return;
  }

  // ---- overview / users ----
  if (method === "GET" && path === "/api/v1/overview") {
    json(res, 200, { nodes: nodes.length, tasks: tasks.length });
    return;
  }
  if (method === "GET" && path === "/api/v1/users") {
    json(res, 200, {
      users: [...users.values()].map((u) => ({
        id: u.id,
        username: "admin",
        display_name: u.display_name,
        status: u.status,
        created_at: u.created_at,
        last_login_at: u.last_login_at,
      })),
    });
    return;
  }

  // ---- nodes ----
  if (method === "GET" && path === "/api/v1/nodes") {
    json(res, 200, { nodes, has_more: false });
    return;
  }
  if (method === "POST" && path === "/api/v1/nodes") {
    const body = await readBody();
    if (!body.display_name) {
      json(res, 400, errorBody("invalid_request", "Görünen ad gerekli.", 400));
      return;
    }
    const node = {
      id: uuid(),
      display_name: body.display_name,
      hostname: body.hostname || null,
      os_family: body.os_family || "unknown",
      os_name: body.os_name ?? null,
      os_version: body.os_version ?? null,
      arch: body.arch ?? null,
      agent_version: null,
      status: "pending",
      tags: Array.isArray(body.tags) ? body.tags : [],
      group_ids: Array.isArray(body.group_ids) ? body.group_ids : [],
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      last_seen_at: null,
    };
    nodes.push(node);
    logAudit(actor, "node.create", "node", node.id, "success");
    json(res, 201, node);
    return;
  }

  // ---- groups ----
  if (method === "GET" && path === "/api/v1/node-groups") {
    json(res, 200, { groups });
    return;
  }
  if (method === "POST" && path === "/api/v1/node-groups") {
    const body = await readBody();
    const g = { id: uuid(), name: body.name, description: body.description ?? "", created_at: new Date().toISOString() };
    groups.push(g);
    logAudit(actor, "node.group.create", "node_group", g.id, "success");
    json(res, 201, g);
    return;
  }

  // ---- tasks ----
  if (method === "GET" && path === "/api/v1/task-operations") {
    json(res, 200, { operations: OPERATIONS });
    return;
  }
  if (method === "GET" && path === "/api/v1/tasks") {
    json(res, 200, { tasks, has_more: false });
    return;
  }
  if (method === "POST" && path === "/api/v1/tasks") {
    const body = await readBody();
    if (!OPERATIONS.some((o) => o.name === body.operation)) {
      json(res, 400, errorBody("unsupported_operation", "Desteklenmeyen operasyon.", 400));
      return;
    }
    const task = {
      id: uuid(),
      operation: body.operation,
      operation_version: "1.0.0",
      node_id: body.node_id || null,
      payload: body.payload ?? {},
      status: "queued",
      actor,
      correlation_id: randomBytes(8).toString("hex"),
      idempotency_key: body.idempotency_key || null,
      error_category: null,
      attempts: 0,
      max_attempts: 3,
      retry_safe: true,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    tasks.unshift(task);
    logAudit(actor, "task.create", "task", task.id, "success");
    json(res, 201, task);
    return;
  }
  const cancelMatch = /^\/api\/v1\/tasks\/([^/]+)\/cancel$/.exec(path);
  if (method === "POST" && cancelMatch) {
    const t = tasks.find((x) => x.id === cancelMatch[1]);
    if (!t || t.status !== "queued") {
      json(res, 409, errorBody("not_cancellable", "Görev iptal edilemez durumda.", 409));
      return;
    }
    t.status = "cancelled";
    logAudit(actor, "task.cancel", "task", t.id, "success");
    json(res, 200, t);
    return;
  }

    // ---- agent enrollment tokens (mock) ----
  if (method === "GET" && path === "/api/v1/agent-enrollments") {
    json(res, 200, { tokens: enrollmentTokens });
    return;
  }
  if (method === "POST" && path === "/api/v1/agent-enrollments") {
    const body = await readBody();
    const now = new Date().toISOString();
    const ttl = (body.ttl_minutes ?? 15) * 60_000;
    const tok = {
      id: uuid(),
      scope_type: body.scope_type === "node" ? "node" : "group",
      node_id: body.node_id || null,
      group_id: body.group_id || null,
      created_by: actor,
      expires_at: new Date(Date.now() + ttl).toISOString(),
      consumed_at: null,
      revoked_at: null,
      created_at: now,
    };
    enrollmentTokens.unshift(tok);
    logAudit(actor, "agent.token.issue", "enrollment_token", tok.id, "success");
    // Plaintext yalnızca oluşturma yanıtında bir kez döner (gerçek API gibi).
    const plaintext = "e2e-" + tok.id.slice(0, 8) + "-mock-token";
    json(res, 201, { token: tok, plaintext, expires_at: tok.expires_at, warning: "tek gösterim" });
    return;
  }
  const revokeMatch = /^\/api\/v1\/agent-enrollments\/([^/]+)\/revoke$/.exec(path);
  if (method === "POST" && revokeMatch) {
    const tok = enrollmentTokens.find((x) => x.id === revokeMatch[1] && !x.consumed_at && !x.revoked_at);
    if (!tok) {
      json(res, 404, errorBody("not_found", "Token bulunamadı.", 404));
      return;
    }
    tok.revoked_at = new Date().toISOString();
    logAudit(actor, "agent.token.revoke", "enrollment_token", tok.id, "success");
    json(res, 200, { status: "ok" });
    return;
  }

  // ---- audit ----
  if (method === "GET" && path === "/api/v1/audit") {
    const action = url.searchParams.get("action");
    const filtered = action ? audit.filter((e) => e.action === action) : audit;
    json(res, 200, { entries: filtered.slice(0, 50), has_more: false });
    return;
  }

  json(res, 404, errorBody("not_found", "İstenen kaynak bulunamadı.", 404));
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`e2e mock api listening on http://127.0.0.1:${PORT}`);
});
