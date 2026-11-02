# Authentication

Wattson has local users (username and password), optional OpenID Connect (OIDC) sign-in and API tokens.

- **First run.** The setup wizard (`POST /api/setup`; the web UI wraps it) is open until the first user exists. That user is the bootstrap admin. The server logs a `setup required` warning with the URL. Open it before exposing the port.
- **Sessions** are a `wattson_session` cookie (SameSite=Lax, HttpOnly), valid 168 h by default (`auth.session_ttl_hours`). Changing a password revokes all of that user's sessions.
- **Scopes** are `read`, `control` and `admin` (admin implies the other two). A session holds read and control, plus admin for admins.
- The bootstrap admin can always sign in with a password, even when `auth.allow_local_login` is off.

## OIDC with Authentik

1. In Wattson set the public URL (`PATCH /api/settings {"general":{"public_url":"https://wattson.example.com"}}`). The redirect URI is built from it.
2. In Authentik create a **Provider**, type *OAuth2/OpenID Provider*:
   - Client type *Confidential*.
   - **Redirect URIs/Origins**: `https://wattson.example.com/api/auth/oidc/authentik/callback` (strict match). Use the `redirect_uri` that Wattson returns when you add the provider; the part after `oidc/` is the slug you choose.
   - **Scopes**: `openid`, `profile`, `email` and a groups scope mapping (Authentik's default `profile` mapping already emits `groups`; otherwise add the *groups* property mapping).
   - **Signing key**: pick a certificate so ID tokens are signed with RS256. Wattson verifies the signature against the provider's JWKS.
   - Enable **Include claims in id_token**. If you leave it off, Wattson falls back to the userinfo endpoint for groups and email.
3. Create an **Application** that uses the provider. Its policy/group binding is the gate for who may sign in: with `auto_create_users` (the default) every user the application admits gets an account.
4. Copy the client ID and secret. Add the provider (as admin, with the session cookie or an `admin` token):

   ```
   curl -sS -X POST https://wattson.example.com/api/admin/oidc-providers \
     -H "Authorization: Bearer $WATTSON_ADMIN_TOKEN" -H 'Content-Type: application/json' \
     -d '{"slug":"authentik","display_name":"Authentik",
          "discovery_url":"https://auth.example.com/application/o/wattson/",
          "client_id":"…","client_secret":"…",
          "scopes":"openid profile email groups","admin_group":"wattson-admins"}'
   ```

   The discovery URL is the application's *OpenID Configuration Issuer* (keep its trailing slash) or the full `.well-known/openid-configuration` URL. `POST /api/admin/oidc-providers/discover` checks a URL without saving anything.
5. Set `admin_group` to the Authentik group whose members should be admins. Admin status follows the group, except that the bootstrap admin and the last remaining admin are never demoted.
6. The sign-in page now shows an *Authentik* button.

Provider options: `allowed_groups`, `allowed_emails` (empty allows all), `groups_claim` (default `groups`), `link_by_email` (link a first sign-in to a local user with the same verified email; refused for admins), `auto_create_users`, `enabled`. The client secret is sealed at rest and never returned.

## Keycloak, Authelia, Pocket ID

Any OIDC provider with discovery works. What you need at the provider:

| | Value |
|---|---|
| Client type | confidential, authorization code flow with PKCE (S256) |
| Redirect URI | `https://<public url>/api/auth/oidc/<slug>/callback` |
| Scopes | `openid profile email`, plus the one that carries groups |
| Discovery URL | Keycloak `https://kc.example.com/realms/<realm>`; Authelia `https://auth.example.com`; Pocket ID `https://id.example.com` |

- **Keycloak:** add a *Group Membership* mapper (token claim name `groups`, full group path off) to the client scope so groups arrive in the ID token.
- **Authelia:** define the client under `identity_providers.oidc.clients` with `authorization_policy`, the redirect URI above and scopes `openid profile email groups`.
- **Pocket ID:** create an OIDC client, copy its ID and secret and set the callback URL; assign user groups to the client to restrict access.

If groups are not in the ID token Wattson asks the userinfo endpoint. Without a groups claim, `allowed_groups` rejects everyone and `admin_group` never matches, so leave them empty or fix the mapper.

## Behind a reverse proxy (Authentik outpost, Traefik, nginx, Caddy)

By default Wattson ignores `X-Forwarded-For` and `X-Forwarded-Proto`: any client can set them. Behind a proxy that means every login attempt shares the proxy's address (one rate-limit bucket for everyone) and the session cookie is not marked `Secure` unless `general.public_url` is https.

List your proxies in `auth.trusted_proxies` (admin, through `PATCH /api/settings`), as CIDRs or single IPs:

```
curl -sS -X PATCH https://wattson.example.com/api/settings \
  -H "Authorization: Bearer $WATTSON_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"auth":{"trusted_proxies":["172.18.0.0/16"]}}'
```

- The default is empty: the headers are ignored. At most 64 entries. `[]` clears the list.
- Use the address the proxy connects *from* (for Docker, the proxy container's network, such as `172.18.0.0/16`), not the public address. Never trust `0.0.0.0/0`: that lets any client spoof its address.
- Only when the TCP peer is in the list are the headers believed. The client address is then the rightmost `X-Forwarded-For` entry that is not itself a trusted proxy (entries further left are client-supplied and ignored). That address keys the login rate limit.
- `X-Forwarded-Proto: https` from a trusted proxy marks the session and OIDC state cookies `Secure` and makes the OIDC redirect URI https when `general.public_url` is unset. Setting `general.public_url` is still the recommended way to pin the redirect URI.
- `X-Forwarded-Host` is never used. If the proxy rewrites `Host`, set `general.public_url`.
- Make the proxy overwrite or append to `X-Forwarded-For` (nginx `proxy_add_x_forwarded_for`, Traefik and Caddy do this by default).

With Authentik the proxy is whatever sits in front of Wattson (Traefik, nginx, Caddy), not Authentik itself: Wattson talks OIDC to Authentik directly and does not use the Authentik forward-auth outpost, so do not put Wattson behind forward auth as well, or API tokens and the WebSocket will be blocked.

## API tokens and kiosks

Tokens are for scripts, Home Assistant and kiosk displays. They look like `wat_…` and are shown once.

```
curl -sS -X POST https://wattson.example.com/api/tokens \
  -H "Authorization: Bearer $WATTSON_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"living-room kiosk","scopes":["read","control"]}'
```

- Only a signed-in session can mint tokens, and only within its own scopes. A token cannot mint tokens.
- A **kiosk** (a tablet or Pi next to the bike) gets a token with `read` and `control`; no `admin`. Put it in the kiosk's configuration, not in a URL. Revoke it from the token list (`DELETE /api/tokens/{id}`, which needs the `control` scope) and it stops working within a minute, including open WebSockets.
- Browsers cannot set headers on a WebSocket, so send the token as a subprotocol: `bearer.<token>` next to `wattson.v1`. See [api.md](api.md).
- Any user with `control` may drive the one bike.

## Locked out? Break-glass commands

Both commands work on the data directory (`--data-dir`, or `WATTSON_DATA_DIR`, default `/data`). They are safe while the server runs, and take effect at once.

```
wattson admin reset-password <user>   # prints a new random password once, signs the user out everywhere
                                      # (warns on stderr if the user is disabled or local login is off)
wattson admin disable-oidc            # disables every OIDC provider and turns local login back on
```

In Docker: `docker exec wattson wattson admin reset-password admin`.

`disable-oidc` is for a broken or misconfigured identity provider. It leaves the provider rows in place (disabled), so re-enable them from the API once the provider works. A lost or replaced `secret.key` is not recoverable by these commands: sealed secrets (client secrets, tokens) can no longer be opened and the server refuses to start rather than lose data silently. Restore the key from backup, or restore a matching `wattson.db` as well.

## Back up

Copy `wattson.db` (with its `-wal` and `-shm` files, or use `sqlite3 wattson.db ".backup out.db"`) **and** `secret.key` together. Keep the key private (mode 0600).
