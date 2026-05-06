# Sandbox–Account Link Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Connect Sandboxes and Accounts tabs so sandboxes can be created from existing accounts in one click, remove Start/Pause/Stop from the Accounts tab, and auto-seed a second account from env vars on startup.

**Architecture:** All changes are in `agent/public/index.html` (UI) and `agent/server.js` (one new route + one startup block). No new files. The clone endpoint reads the full secret key server-side so it never travels to or from the client again. Env-seeding is idempotent: it checks for an existing account with matching public key before creating.

**Tech Stack:** Vanilla JS (inline in index.html), Express.js (server.js), existing `addAccount`/`getAccountById` from config-store.js.

---

## File Map

| File | What changes |
|---|---|
| `agent/public/index.html` | (1) Remove `controls` var from `renderAccounts()`; (2) Add "From Account" button to Sandboxes panel HTML; (3) Update `renderSandboxesTab()` to manage button disabled state; (4) Add `sandbox-from-account` branch in `showModal()`; (5) Add `submitSandboxFromAccount()` function |
| `agent/server.js` | (6) Add `POST /api/accounts/:id/clone` route after the existing accounts routes; (7) Add env-seeding block after `loadConfig()` |
| `.env.example` | (8) Add commented-out second-account env vars |

---

## Task 1: Remove runtime controls from Accounts tab

**Files:**
- Modify: `agent/public/index.html` (around line 3184)

The `renderAccounts()` function builds each account card with a `controls` string that holds Start/Pause/Stop buttons. Remove that variable and its usage from the card HTML.

- [ ] **Step 1: Remove the `controls` variable and its insertion in `renderAccounts()`**

Find this block in `renderAccounts()` (around line 3184):

```javascript
    const controls = running
      ? '<button class="btn sm" onclick="pauseSandbox(\'' + sandboxId + '\')">Pause</button><button class="btn sm danger" onclick="stopSandbox(\'' + sandboxId + '\')">Stop</button>'
      : '<button class="btn sm primary" onclick="startSandbox(\'' + sandboxId + '\')">Start</button>';
```

Delete those three lines entirely.

Then find the card actions row (a few lines below, inside the `return` string):

```javascript
      + '<button class="btn sm" onclick="configureSandbox(\'' + sandboxId + '\')">Configure</button>'
      + controls
      + '<button class="btn sm danger" onclick="deleteAccount(\'' + a.id + '\')">Delete</button>'
```

Remove the `+ controls` line, leaving:

```javascript
      + '<button class="btn sm" onclick="configureSandbox(\'' + sandboxId + '\')">Configure</button>'
      + '<button class="btn sm danger" onclick="deleteAccount(\'' + a.id + '\')">Delete</button>'
```

- [ ] **Step 2: Manual verify**

Open the app in a browser. Navigate to the Accounts tab. Each account card should show **View**, **Configure**, **Delete** — no Start, Pause, or Stop buttons. Status badges (Running/Ready/Stopped) and phase/beats info should still appear.

- [ ] **Step 3: Commit**

```bash
git add agent/public/index.html
git commit -m "feat: remove runtime controls from accounts tab"
```

---

## Task 2: Add "From Account" button to Sandboxes tab header

**Files:**
- Modify: `agent/public/index.html` (around line 1125 static HTML, and `renderSandboxesTab()` around line 2190)

The Sandboxes panel header is static HTML. Add a second button. Then update `renderSandboxesTab()` to control the disabled state based on whether any accounts exist.

- [ ] **Step 1: Update the Sandboxes panel HTML header**

Find this block in the static HTML (around line 1125):

```html
        <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">
          <div class="section-title" style="margin:0">Active Sandboxes</div>
          <button class="btn primary sm" onclick="showModal('sandbox-create')">New Sandbox</button>
        </div>
```

Replace it with:

```html
        <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">
          <div class="section-title" style="margin:0">Active Sandboxes</div>
          <div style="display:flex;gap:8px">
            <button class="btn primary sm" onclick="showModal('sandbox-create')">New Sandbox</button>
            <button class="btn sm" id="btn-from-account" onclick="showModal('sandbox-from-account')">From Account</button>
          </div>
        </div>
```

- [ ] **Step 2: Update `renderSandboxesTab()` to manage disabled state**

At the end of `renderSandboxesTab()`, just before the closing `}`, add:

```javascript
  const btnFromAccount = document.getElementById('btn-from-account');
  if (btnFromAccount) {
    const hasAccounts = (config.accounts || []).length > 0;
    btnFromAccount.disabled = !hasAccounts;
    btnFromAccount.title = hasAccounts ? '' : 'Add an account first in the Accounts tab';
  }
```

- [ ] **Step 3: Manual verify**

Navigate to the Sandboxes tab. Two buttons appear side by side: **New Sandbox** (primary/filled) and **From Account** (outlined). If no accounts exist, "From Account" is grayed out and has the tooltip text when hovered. If accounts exist, the button is enabled.

- [ ] **Step 4: Commit**

```bash
git add agent/public/index.html
git commit -m "feat: add From Account button to sandboxes tab header"
```

---

## Task 3: Add `POST /api/accounts/:id/clone` backend endpoint

**Files:**
- Modify: `agent/server.js` (after line 1184, after the existing accounts routes)

The clone endpoint reads the full account record server-side (including the unmasked secret key), creates a new account with a new name via `addAccount()`, and returns the new sandbox ID.

- [ ] **Step 1: Add the clone route in `server.js`**

Find the end of the existing accounts routes block (after the `activate` route, around line 1184):

```javascript
app.post('/api/accounts/:id/activate', async (req, res) => {
  // ... existing activate logic ...
  res.json({ ok: true });
});
```

Insert the following new route immediately after that closing `});`:

```javascript
app.post('/api/accounts/:id/clone', async (req, res) => {
  try {
    const source = getAccountById(req.params.id);
    if (!source) return res.status(404).json({ error: 'Account not found' });
    const { name } = req.body;
    if (!name?.trim()) return res.status(400).json({ error: 'Name is required' });
    const account = await addAccount({
      name: name.trim(),
      publicKey: source.publicKey,
      secretKey: source.secretKey,
      baseUrl: source.baseUrl,
      paper: source.paper,
    });
    broadcast('config', safeConfig());
    res.json({ ok: true, sandboxId: `sbx_${account.id}`, account: { ...account, secretKey: '****' + account.secretKey.slice(-4) } });
  } catch (err) { res.status(400).json({ error: err.message }); }
});
```

- [ ] **Step 2: Verify the endpoint manually with curl**

With the server running, replace `<SOURCE_ACCOUNT_ID>` with an actual account ID visible in the Accounts tab (first 8 chars shown), and run:

```bash
curl -s -X POST http://localhost:3737/api/accounts/<SOURCE_ACCOUNT_ID>/clone \
  -H "Content-Type: application/json" \
  -d '{"name":"Cloned Sandbox"}' | jq .
```

Expected response shape:
```json
{
  "ok": true,
  "sandboxId": "sbx_xxxxxxxx",
  "account": { "id": "xxxxxxxx", "name": "Cloned Sandbox", "secretKey": "****XXXX", ... }
}
```

The Accounts tab and Sandboxes tab should both show the new entry after refresh.

- [ ] **Step 3: Verify idempotency is not a concern**

The clone creates a brand-new account with a new UUID — each call produces a distinct account. This is correct: users may intentionally create multiple sandboxes from the same credentials.

- [ ] **Step 4: Commit**

```bash
git add agent/server.js
git commit -m "feat: add POST /api/accounts/:id/clone endpoint"
```

---

## Task 4: Add "From Account" modal and submit function

**Files:**
- Modify: `agent/public/index.html` (`showModal()` around line 3282, and add `submitSandboxFromAccount()` near `submitSandboxCreate()` around line 2352)

- [ ] **Step 1: Add the `sandbox-from-account` branch in `showModal()`**

Find the `showModal` function (around line 3278). It has a chain of `if(type === '...')` / `else if` branches. Find the first `else if` after the `sandbox-create` branch:

```javascript
  else if(type === 'account-create') {
```

Insert a new `else if` branch immediately before it:

```javascript
  else if(type === 'sandbox-from-account') {
    const accounts = config.accounts || [];
    const agentOpts = (config.agents || []).map(a => '<option value="'+esc(a.id)+'">'+esc(a.name)+'</option>').join('');
    const profiles = Array.isArray(window._hbProfiles) ? window._hbProfiles : [];
    const profileOpts = profiles.map(p => '<option value="'+esc(p.id)+'">'+esc(p.name)+'</option>').join('');
    const accountOpts = accounts.map(a =>
      '<option value="'+esc(a.id)+'">' + esc(a.name) + ' (' + (a.paper !== false ? 'Paper' : 'Live') + ')</option>'
    ).join('');
    modal.innerHTML = '<h3>New Sandbox from Account</h3>'
      + '<div class="form-group"><label>Sandbox Name</label><input type="text" id="f-sfa-name" placeholder="e.g. PennyProphet Paper"></div>'
      + '<div class="form-group"><label>Account</label><select id="f-sfa-account"><option value="">-- select account --</option>'+accountOpts+'</select></div>'
      + (agentOpts ? '<div class="form-group"><label>Assign Agent (optional)</label><select id="f-sfa-agent"><option value="">None</option>'+agentOpts+'</select></div>' : '')
      + (profileOpts ? '<div class="form-group"><label>Heartbeat Profile (optional)</label><select id="f-sfa-profile"><option value="">None</option>'+profileOpts+'</select></div>' : '')
      + '<div class="modal-actions"><button class="btn" onclick="closeModal()">Cancel</button><button class="btn primary" onclick="submitSandboxFromAccount()">Create Sandbox</button></div>';
  }
```

- [ ] **Step 2: Add `submitSandboxFromAccount()` function**

Find `submitSandboxCreate()` (around line 2352). Add the following new function immediately after its closing `}`:

```javascript
async function submitSandboxFromAccount() {
  const name = document.getElementById('f-sfa-name')?.value?.trim() || '';
  const accountId = document.getElementById('f-sfa-account')?.value || '';
  const agentId = document.getElementById('f-sfa-agent')?.value || '';
  const profileId = document.getElementById('f-sfa-profile')?.value || '';
  if (!name || !accountId) { showToast('Name and account are required', 'error'); return; }
  try {
    const r = await fetch('/api/accounts/' + encodeURIComponent(accountId) + '/clone', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    });
    const d = await r.json();
    if (d.error) { showToast('Error: ' + d.error, 'error'); return; }
    const sandboxId = d.sandboxId;
    if (sandboxId && agentId) {
      await fetch('/api/sandboxes/' + encodeURIComponent(sandboxId) + '/agent', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ activeAgentId: agentId }),
      });
    }
    if (sandboxId && profileId) {
      await fetch('/api/heartbeat/apply-profile', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ sandboxId, profile: profileId }),
      });
    }
    closeModal();
    showToast('Sandbox created', 'success');
  } catch (e) { showToast('Error: ' + e.message, 'error'); }
}
```

- [ ] **Step 3: Manual verify — happy path**

1. Go to Sandboxes tab. Click "From Account".
2. Modal opens titled "New Sandbox from Account" with Name field, Account dropdown (listing accounts as "Name (Paper/Live)"), optional Agent and Profile dropdowns.
3. Fill in a name and pick an account. Click "Create Sandbox".
4. Toast shows "Sandbox created". Modal closes. New sandbox card appears in the Sandboxes tab with the chosen name.
5. New account also appears in the Accounts tab (same credentials, new name).

- [ ] **Step 4: Manual verify — validation**

1. Click "From Account", leave Name blank, pick an account, click Create. Toast shows "Name and account are required".
2. Click "From Account", fill Name, leave Account at "-- select account --", click Create. Toast shows "Name and account are required".

- [ ] **Step 5: Commit**

```bash
git add agent/public/index.html
git commit -m "feat: add From Account modal and submitSandboxFromAccount"
```

---

## Task 5: Env-seeded second account + `.env.example`

**Files:**
- Modify: `agent/server.js` (after `loadConfig()` call around line 210)
- Modify: `.env.example`

- [ ] **Step 1: Add env-seeding block in `server.js`**

Find this block (around line 209–217):

```javascript
// ── Load Config ────────────────────────────────────────────────────
await loadConfig();
const initialActiveAccount = getActiveAccount();
if (initialActiveAccount?.id) {
  const migration = await migrateLegacyDataForAccount(initialActiveAccount.id);
  if (migration.migrated) {
    console.log(`  Migrated legacy data into sandbox for account ${initialActiveAccount.id}: ${migration.copied.join(', ')}`);
  }
}
```

Insert the following block immediately after that entire `if` block (after its closing `}`):

```javascript
// Seed second account from env vars if configured
{
  const pk2 = process.env.ALPACA_PUBLIC_KEY_2;
  const sk2 = process.env.ALPACA_SECRET_KEY_2;
  if (pk2 && sk2) {
    const cfg = getConfig();
    const alreadyExists = (cfg.accounts || []).some(a => a.publicKey === pk2);
    if (!alreadyExists) {
      const paper2 = process.env.ALPACA_PAPER_2 !== 'false';
      const name2 = process.env.ALPACA_NAME_2 || 'Account 2';
      const baseUrl2 = process.env.ALPACA_ENDPOINT_2 ||
        (paper2 ? 'https://paper-api.alpaca.markets' : 'https://api.alpaca.markets');
      await addAccount({ name: name2, publicKey: pk2, secretKey: sk2, baseUrl: baseUrl2, paper: paper2 });
      console.log(`  Seeded second account "${name2}" from env vars`);
    }
  }
}
```

Note: `getConfig` is already imported at the top of `server.js` (line 21). `addAccount` is also already imported.

- [ ] **Step 2: Update `.env.example`**

Open `.env.example`. Append the following block at the end of the file:

```
# Second Alpaca account (optional — auto-creates a second sandbox on startup if not already present)
# ALPACA_NAME_2=Account 2
# ALPACA_PUBLIC_KEY_2=your_second_public_key
# ALPACA_SECRET_KEY_2=your_second_secret_key
# ALPACA_ENDPOINT_2=https://paper-api.alpaca.markets
# ALPACA_PAPER_2=true
```

- [ ] **Step 3: Manual verify — seeding runs once**

Add real credentials to `.env`:
```
ALPACA_PUBLIC_KEY_2=AKTEST123
ALPACA_SECRET_KEY_2=secrettest
ALPACA_NAME_2=My Second Account
```

Restart the server. Console should print:
```
  Seeded second account "My Second Account" from env vars
```

The Accounts tab should show the new account. Sandboxes tab should show a new sandbox for it.

- [ ] **Step 4: Manual verify — idempotent on restart**

Restart the server again with the same env vars. The console line should NOT appear again. No duplicate account should be created.

- [ ] **Step 5: Commit**

```bash
git add agent/server.js .env.example
git commit -m "feat: seed second alpaca account from env vars on startup"
```

---

## Self-Review Checklist

- **Spec § 1 (From Account button):** Covered in Task 2 — button added with disabled state.
- **Spec § 2 (From Account modal):** Covered in Task 4 — modal + submit with clone endpoint.
- **Spec § 3 (Accounts tab cleanup):** Covered in Task 1 — Start/Pause/Stop removed, badges kept.
- **Spec § 4 (Env seeding):** Covered in Task 5 — seeding block + .env.example.
- **Clone endpoint secret key concern:** Addressed in Task 3 — reads from server-side `getAccountById`, never touches masked client value.
- **Disabled button state:** `renderSandboxesTab()` runs on every config refresh, so the button state stays in sync.
- **`submitSandboxFromAccount` uses `d.sandboxId`** (not `d.sandboxId || d.id`) because the clone endpoint always returns `sandboxId` explicitly — no fallback ambiguity.
