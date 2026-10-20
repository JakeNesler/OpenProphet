// Server lifecycle helpers split out of server.js so they can be tested without booting the
// dashboard, spawning the Go backend, or touching brokerage credentials.
import { existsSync, renameSync, rmSync } from 'fs';
import path from 'path';
import { timingSafeEqual } from 'crypto';
import { execFileSync } from 'child_process';

export const GO_BINARY_NAME = 'prophet_bot';

// Token-based auth for the /api router. Express strips the mount path from req.path, so the
// health check must be matched on the full path (baseUrl + path), not req.path alone.
export function createAuthMiddleware(token) {
  return function authMiddleware(req, res, next) {
    if (!token) return next(); // no token configured = open access
    // Allow health check unauthenticated
    if (`${req.baseUrl || ''}${req.path}` === '/api/health') return next();
    // Check Authorization header or query param
    const header = req.headers.authorization;
    const supplied = header?.startsWith('Bearer ') ? header.slice(7) : req.query.token;
    if (typeof supplied === 'string') {
      const a = Buffer.from(supplied);
      const b = Buffer.from(token);
      if (a.length === b.length && timingSafeEqual(a, b)) return next();
    }
    res.status(401).json({ error: 'Unauthorized. Set Authorization: Bearer <token> header.' });
  };
}

function goBuild(outputPath, { cwd }) {
  execFileSync('go', ['build', '-o', outputPath, './cmd/bot'], { cwd, timeout: 120000 });
}

// Build the Go binary if it is missing, or if `force` (e.g. the committed binary was built
// for another OS/arch and cannot exec on this machine). Builds to a temp file beside the
// binary and renames it into place, so a failed rebuild leaves the existing binary intact.
// Returns the binary path or throws.
export function ensureGoBinary({ projectRoot, force = false, build = goBuild, log = console.log }) {
  const binaryPath = path.join(projectRoot, GO_BINARY_NAME);
  if (!force && existsSync(binaryPath)) return binaryPath;
  log(force ? '  Rebuilding Go binary for this platform...' : '  Building Go binary...');
  const tempPath = path.join(projectRoot, `.${GO_BINARY_NAME}.build-${process.pid}-${Date.now()}`);
  try {
    build(tempPath, { cwd: projectRoot });
    if (!existsSync(tempPath)) throw new Error(`Go build produced no binary at ${tempPath}`);
    renameSync(tempPath, binaryPath);
  } finally {
    rmSync(tempPath, { force: true });
  }
  return binaryPath;
}

// config-store makes the first account added the active one, but the server's harness and
// Go backend were bound before any account existed. When the new account becomes active,
// hand it to `activate` (rebind harness, start backend) as switching accounts does.
export async function addAccountAndActivate(input, { addAccount, getActiveAccount, activate }) {
  const previousActiveId = getActiveAccount()?.id || null;
  const account = await addAccount(input);
  const active = getActiveAccount();
  const activated = Boolean(active) && active.id === account.id && previousActiveId !== account.id;
  if (activated) await activate(active);
  return { account, activated };
}
