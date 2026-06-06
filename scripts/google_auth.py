#!/usr/bin/env python3
"""Re-consent helper for the Google Health API (stdlib only — no pip installs).

Stateless two-step OAuth installed-app flow, so it works on a headless box and
under shells that give no interactive stdin (e.g. Claude Code's `!`):

    # Step 1 — print the consent URL:
    python3 scripts/google_auth.py

    # ...approve in your browser, copy the http://localhost/?...&code=... URL...

    # Step 2 — exchange it (QUOTE the URL — it contains & characters):
    python3 scripts/google_auth.py 'http://localhost/?...&code=...'

The new refresh token is written into .env and google_token.json.

Reads client id/secret from .env (GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET) or,
if absent, from google_client_secret.json. Scopes come from .env
GOOGLE_REQUIRED_SCOPES, else default to the two Health scopes.
"""

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ENV_PATH = os.path.join(ROOT, ".env")
CLIENT_JSON = os.path.join(ROOT, "google_client_secret.json")
TOKEN_JSON = os.path.join(ROOT, "google_token.json")

AUTH_URI = "https://accounts.google.com/o/oauth2/v2/auth"
TOKEN_URI = "https://oauth2.googleapis.com/token"
REDIRECT_URI = "http://localhost"  # registered in the installed-app client

DEFAULT_SCOPES = [
    "https://www.googleapis.com/auth/googlehealth.activity_and_fitness.readonly",
    "https://www.googleapis.com/auth/googlehealth.location.readonly",
]


def read_env(path):
    env = {}
    if os.path.exists(path):
        for line in open(path):
            line = line.rstrip("\n")
            if not line or line.lstrip().startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            env[k.strip()] = v
    return env


def update_env(path, updates):
    """Replace/insert keys in .env, preserving all other lines and comments."""
    def fmt(v):
        # Quote values with whitespace so `source .env` (shell) doesn't choke.
        v = str(v)
        return f'"{v}"' if (" " in v and not v.startswith(('"', "'"))) else v

    lines = open(path).read().splitlines() if os.path.exists(path) else []
    seen = set()
    out = []
    for line in lines:
        if "=" in line and not line.lstrip().startswith("#"):
            k = line.split("=", 1)[0].strip()
            if k in updates:
                out.append(f"{k}={fmt(updates[k])}")
                seen.add(k)
                continue
        out.append(line)
    for k, v in updates.items():
        if k not in seen:
            out.append(f"{k}={fmt(v)}")
    open(path, "w").write("\n".join(out) + "\n")


def get_client():
    env = read_env(ENV_PATH)
    cid = env.get("GOOGLE_CLIENT_ID")
    csecret = env.get("GOOGLE_CLIENT_SECRET")
    if cid and csecret:
        return cid, csecret
    data = json.load(open(CLIENT_JSON))["installed"]
    return data["client_id"], data["client_secret"]


def get_scopes():
    env = read_env(ENV_PATH)
    raw = env.get("GOOGLE_REQUIRED_SCOPES", "").strip()
    return raw.split() if raw else DEFAULT_SCOPES


def print_auth_url(client_id, scopes):
    params = {
        "client_id": client_id,
        "redirect_uri": REDIRECT_URI,
        "response_type": "code",
        "scope": " ".join(scopes),
        "access_type": "offline",      # we want a refresh token
        "prompt": "consent",           # force refresh_token re-issue
    }
    auth_url = AUTH_URI + "?" + urllib.parse.urlencode(params)
    print("\nStep 1) Open this URL in your browser and approve:\n")
    print(auth_url)
    print(
        "\nAfter approving, the browser will try to load http://localhost/?...&code=...\n"
        "It will show a 'can't reach this page' error — that's expected.\n"
        "Copy the FULL URL from the address bar, then run (note the single quotes):\n\n"
        "    python3 scripts/google_auth.py '<paste that URL here>'\n"
    )


def main():
    client_id, client_secret = get_client()
    scopes = get_scopes()

    args = sys.argv[1:]
    if not args:
        print_auth_url(client_id, scopes)
        return

    pasted = args[0].strip()
    # Accept either the raw code or the full redirect URL.
    code = pasted
    if pasted.startswith("http"):
        qs = urllib.parse.urlparse(pasted).query
        code = urllib.parse.parse_qs(qs).get("code", [""])[0]
    if not code:
        print("ERROR: could not find an authorization code.", file=sys.stderr)
        sys.exit(1)

    body = urllib.parse.urlencode({
        "code": code,
        "client_id": client_id,
        "client_secret": client_secret,
        "redirect_uri": REDIRECT_URI,
        "grant_type": "authorization_code",
    }).encode()

    req = urllib.request.Request(TOKEN_URI, data=body, method="POST")
    try:
        resp = json.load(urllib.request.urlopen(req))
    except urllib.error.HTTPError as e:
        print("Token exchange failed:\n" + e.read().decode(), file=sys.stderr)
        sys.exit(1)

    refresh = resp.get("refresh_token")
    access = resp.get("access_token", "")
    expires_in = resp.get("expires_in")
    granted = resp.get("scope", " ".join(scopes))

    if not refresh:
        print(
            "WARNING: no refresh_token returned (Google omits it if you've "
            "consented before without revoking). Revoke prior access at "
            "https://myaccount.google.com/permissions and re-run, or it was "
            "issued earlier.",
            file=sys.stderr,
        )

    updates = {
        "GOOGLE_ACCESS_TOKEN": access,
        "GOOGLE_GRANTED_SCOPES": granted,
    }
    if refresh:
        updates["GOOGLE_REFRESH_TOKEN"] = refresh
    update_env(ENV_PATH, updates)

    if refresh:
        json.dump({
            "type": "authorized_user",
            "client_id": client_id,
            "client_secret": client_secret,
            "refresh_token": refresh,
            "token_uri": TOKEN_URI,
            "scopes": granted.split(),
        }, open(TOKEN_JSON, "w"), indent=2)

    print("\nDone. Updated .env" + (" and google_token.json" if refresh else ""))
    print("Granted scopes:", granted)


if __name__ == "__main__":
    main()
