# Two-Person Integrity for Shared Credentials

Status: core implemented + independently reviewed. Wiring (tools/TUI/CLI) pending.

## Goal

Share project/global credentials between the TUI and the agent harness, across
users, **without any secret in cleartext and without any infrastructure**, and
require a **second human approver** before a sensitive credential can be used.

## Threat model

- The agent is untrusted (prompt-injectable) → it must never see plaintext.
- The git repo is semi-public → only ciphertext + public keys are committed.
- A single machine may be fully compromised → it must not be able to use a
  sensitive credential alone.
- Insider / over-broad access → sensitive use needs a distinct second approver.

## Design: two tiers by sensitivity

| Tier | Mechanism | Decrypt rule | Use for |
|------|-----------|--------------|---------|
| Normal | `MultiRecipientStorage` (age X25519) | any 1 recipient | day-to-day creds |
| Sensitive | **Two-Person Integrity** (this doc) | **≥ 2 distinct recipients** | prod / AWS / SSH / tagged |

Identity substrate: per-user age X25519 identity (private key `0600`, never
leaves the machine); SSH Ed25519 keys may back the identity/signing. Public keys
live in `recipients.txt` (committed). Everything shareable is ciphertext.

## How Two-Person Integrity works

**Seal** (`twoperson.go`):
1. Random 32-byte data key `DK`.
2. `ciphertext = XChaCha20-Poly1305(DK, nonce, secret, AAD=credID)`.
3. `shares = ShamirSplit(DK, N, K=2)` over GF(2^8) (`shamir.go`).
4. Each `share_i` is age-encrypted to `recipient_i`'s public key.
5. Persisted as JSON (`twoperson_storage.go`) — **no plaintext, no
   single-holder-decryptable key**. Safe to commit to git.

**Use** (`twoperson_broker.go`, extends the existing `ApprovalError`/`Approve`):
1. Requester calls `vault_exec`. Their identity decrypts **one** share → not enough.
2. Broker returns a pending request (id, cred, canonical command+args, TTL).
3. A **distinct** approver (different roster key, enforced) contributes a second
   share via `swarmos vault approve <id>`.
4. Broker combines ≥ K shares → `DK` → AEAD-open → inject → run → redact →
   **zeroize** `DK` and secret. Grant is scoped to that command+args with a TTL.

## What is enforced vs. trusted

| Property | Guarantee |
|----------|-----------|
| One machine can't use a sensitive cred alone | **Cryptographic** (Shamir 2-of-N) |
| Approver ≠ requester, both in roster | Enforced (age recipient key identity) |
| Agent never sees plaintext | Proxy execution + redaction |
| Envelope bound to its credential | AEAD AAD = credential ID |
| Tamper detected | AEAD authentication |
| Two-person cred can't leak via normal path | **Interlock**: `Execute` returns `ErrTwoPersonRequired`; empty-secret backstop |
| Revoke future use | Remove from `recipients.txt`, re-seal |
| Revoke history | Requires **secret rotation** — crypto cannot un-share git history |

## Operational caveats (accepted)

- **Availability trade-off**: 2-of-N blocks sensitive ops when approvers are
  offline. Use N large enough (e.g. 2-of-5); never 2-of-2 for real teams.
- **Roster hygiene**: one identity per human. A person holding two roster
  identities defeats 2-of-N — provisioning must not issue two slots to one person.
- **Go zeroization is best-effort**: `chacha20poly1305.NewX` copies the key
  internally and the GC may copy buffers; `zeroize` cannot scrub those copies.
- **Not constant-time**: GF(2^8) table lookups are data-dependent; acceptable
  for an offline envelope with cooperative reconstruction.

## Review status

Three independent expert reviews (cryptography, security-protocol, integration):
crypto sound, separation-of-duties holds, concurrency correct. Findings fixed:
bounded pending map + expiry sweep, reconstruct TTL re-check, coeffs zeroization.
The critical empty-secret-injection path is closed by the `Execute` interlock.
40 vault tests pass under `-race`.

## Wired surfaces (implemented + reviewed)

1. Agent tools: `vault_exec` returns `needs_two_person`+`twoPersonRequestId`;
   `vault_approve` lets a DISTINCT second person contribute the 2nd share; retry
   `vault_exec{twoPersonRequestId}` finalizes and runs the immutable original
   command. `vault_add threshold>=2` (or `sensitive` tag) seals via the roster.
2. Headless + daemon auto-load: `vault.AutoLoadProvider` runs non-interactively
   on startup (no passphrase), layering the two-person store in front of the
   team vault so sensitive and normal creds coexist.
3. Roster UX: `swarmos vault identity|members|allow|revoke` with re-seal + rotate
   warnings.

Three-expert review (security, agent-contract, integration) confirmed a single
principal cannot defeat 2-of-N through the tool surface. Fixed: layered-storage
precedence, 0600 perms, provider usability gate. 48 vault tests pass under
`-race`.

## Remaining polish (optional)

- Agent-visible `vault_two_person_status{requestId}` tool (today the approver's
  `satisfied:true` is the retry signal).
- TUI `/vault approve` panel action (CLI + tools cover the flow today).
