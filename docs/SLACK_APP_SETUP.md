<p align="right"><a href="SLACK_APP_SETUP.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Slack App Setup

This guide walks through creating the Slack App that KuruOps's Settings → Conectores → Slack integration connects to. It covers what exists **today** — connecting/disconnecting a workspace via bot-token OAuth — plus scopes reserved for the fuller Slack integration (opening incidents from Slack, syncing thread messages/files into alerts and incidents, linking incident channels) planned for later updates. You only need to do this once per KuruOps deployment.

## 1. Create the app from a manifest

Slack App Manifests let you declare scopes and OAuth settings in one step instead of clicking through several screens. Go to [api.slack.com/apps](https://api.slack.com/apps) → **Create New App** → **From an app manifest**, pick your workspace, and paste the YAML below — replacing `your-kuruops-domain.example.com` with your deployment's actual `APP_BASE_URL`:

```yaml
display_information:
  name: KuruOps
  description: Incident response and SOC operations, connected to Slack.
  background_color: "#1a1a2e"

oauth_config:
  redirect_urls:
    - https://your-kuruops-domain.example.com/auth/oauth/slack/callback
  scopes:
    bot:
      - chat:write
      - channels:read
      - channels:manage
      - channels:history
      - groups:read
      - groups:write
      - groups:history
      - files:read
      - im:write
      - commands

settings:
  org_deploy_enabled: false
  socket_mode_enabled: false
  token_rotation_enabled: false
```

**Why every scope is requested up front, even for a "foundation only" release:** Slack requires a workspace to re-authorize the app any time its scope list changes — connecting again, re-approving on the consent screen. Declaring the full set this integration will eventually need, rather than adding scopes PR by PR, means you connect KuruOps to Slack once and don't have to repeat that step (and re-share the resulting bot token) every time a new capability ships. Not every scope above is exercised by the code that exists today — see the table below.

**Notice what's deliberately absent:** there is no `event_subscriptions` block in the manifest. Slack requires a live URL that can answer a verification challenge the instant `event_subscriptions` is configured with a Request URL — and this release has no inbound receiver to answer it. That block gets added (and this doc updated) in whichever future release adds Slack Events API support (inbound messages, thread replies, slash commands).

| Scope | Used by (today / planned) |
|---|---|
| `chat:write` | Planned — posting messages from KuruOps into a linked channel |
| `channels:read` | Planned — reading public channel metadata |
| `channels:manage` | Planned — creating a channel when an admin links one to an incident |
| `channels:history` | Planned — pulling a public channel's message history |
| `groups:read` | Planned — same as `channels:read`, for private channels |
| `groups:write` | Planned — same as `channels:manage`, for private channels |
| `groups:history` | Planned — same as `channels:history`, for private channels |
| `files:read` | Planned — pulling files attached to a synced thread |
| `im:write` | Planned — direct-messaging an analyst |
| `commands` | Planned — a future `/kuruops` slash command |

None of these are called by KuruOps's code yet — the current release only exchanges the OAuth code for a bot token and stores it. This table exists so a future update to this doc (when a scope actually goes live) has a clear "before" to diff against.

⚠️ **Check this list against Slack's current scope catalog before installing.** Slack's granular-scope naming has changed over time; if any of the names above come back rejected or deprecated when you paste the manifest, check [api.slack.com/scopes](https://api.slack.com/scopes) for the current equivalent and adjust.

## 2. Install the app to your workspace

On the app's **OAuth & Permissions** page, click **Install to Workspace** and approve the requested scopes. This creates the app's installation in your workspace — KuruOps itself hasn't been told about it yet; that happens in the next step, from inside KuruOps.

## 3. Copy credentials into KuruOps's configuration

From the app's **Basic Information** page, copy:

- **Client ID** → `SLACK_CLIENT_ID`
- **Client Secret** → `SLACK_CLIENT_SECRET`
- **Signing Secret** → `SLACK_SIGNING_SECRET`

Set these as environment variables on the `api` service (see `docker-compose.yml` / your deployment's env configuration) and restart it. `SLACK_SIGNING_SECRET` isn't used by anything yet — there's no inbound Slack request to verify in this release — but Slack fixes this value at app-creation time regardless, so capturing it now saves reopening this page later.

## 4. Connect the workspace from KuruOps

In KuruOps, go to **Settings → Conectores → Slack** and click **Connect to Slack**. You'll be redirected to Slack's consent screen, then back to KuruOps showing the connected workspace's name and who connected it. **Disconnect** on the same page removes the stored bot token and turns the integration off — every Slack-dependent feature (including the ones marked "Planned" above, once they exist) stays hidden until a workspace is connected again.

## What's next

This release covers connect/disconnect only. Message/thread/file sync, opening incidents from Slack, and incident-channel linking are planned for later updates — each will extend this same Slack App (reusing the scopes already declared above) rather than requiring a new one.
