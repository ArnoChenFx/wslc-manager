# WSLC Manager

A desktop app for managing [WSL containers](https://learn.microsoft.com/windows/wsl/wsl-container) (`wslc`) on Windows — think Docker Desktop, but for `wslc`.

Built with **Go** and [MyGo](https://github.com/egoist/mygo)'s native UI: no webview, no Electron, no Node. One executable, starts instantly.

<p align="center">
  <img src="resources/icon.png" width="120" alt="WSLC Manager icon">
</p>

<p align="center">
  <a href="README-zh.md">中文文档</a>
</p>

---

## What it does

`wslc` is a command-line tool shipped with WSL for running containers. WSLC Manager puts a real interface on it.

### Containers
List, start, stop, restart and remove containers. Live resource stats, `inspect` details and streaming logs — all in one window, with a resizable split panel.

### Images
Browse local images with sizes and creation dates. Pull images with **streaming progress**, inspect tags in the same repository, jump to details or resource usage, and update one image or all of them at once.

### Volumes and networks
Create, inspect and delete named volumes and networks. Built-in networks are protected from accidental deletion.

### Compose projects
Point the app at a directory containing `compose.yaml` (or `docker-compose.yml`) and it manages the whole stack: start, stop, restart, build, pull, remove, and per-service control.

Orchestration is **built in** — there is no extra tool to install. Compose files are read by [compose-go](https://github.com/compose-spec/compose-go), the same library `docker compose` uses, so variable interpolation, `.env` files, `include`, `extends`, multiple `-f` files and profiles follow the Compose specification. The app and the actions it runs share one loader, so what you see is what runs.

Supported: `up` / `down` / `start` / `stop` / `restart` / `pull` / `build`, `depends_on` conditions (`service_healthy`, `service_completed_successfully`), health checks, multiple networks with aliases and static IPs, secrets and configs (file-backed), `scale`, and a dry-run preview. A service that fails to start does not stop the others; only the services that depend on it are skipped.

Settings that `wslc` cannot honour (`privileged`, `devices`, `sysctls`, restart policies other than `always` / `no`, …) are reported as warnings and ignored; usages that would silently misbehave (anonymous volumes, external or non-file secrets, IPv6 networks) are rejected before anything is created.

The same engine is also available as a standalone [command-line tool](#command-line-tool).

### System
Environment versions, WSL sessions, and a one-click check that can install or update WSL when `wslc` is missing.

### Virtual machine
Everything `wslc` runs lives in a lightweight virtual machine. The **System** page shows whether it is running and how much memory it holds on Windows (the `vmmem…` process in Task Manager), and lets you start and stop it yourself. Stopping it ends the containers running inside; containers and images are kept.

While the virtual machine is stopped, the app asks it for nothing, because even a refresh would wake it up again. The Containers, Images, Volumes and Networks pages say so and offer a **Start VM** button. With no container running, `wslc` also stops the virtual machine by itself after a period of idleness (30 seconds by default), so a virtual machine you start with nothing in it goes back to sleep after that time; raise `session.idleTimeout` in the wslc settings to keep it longer.

### Updates
The app checks this repository's releases shortly after launch and then once a day, and offers a new version in a small window: **Install Update**, **Remind Me Later** or **Skip This Version**. Updates are signed: the app verifies the signature against a public key built into it before it replaces itself, and downloads only what changed when it can. Check by hand on the **System** page, or switch automatic checks and automatic installs off in **Settings**. The check contacts `github.com`.

Self-updating needs a copy that can write to its own folder. The installer's per-user install and a portable copy in an ordinary folder qualify; a copy under `Program Files` does not, and the System page says so.

### Diagnostics
Found a layout glitch or a button that does nothing? Export a diagnostic report: versions, the exact on-screen coordinates of every element, and recent command and error logs — in one Markdown file. There is also a live runtime log that records every `wslc` invocation.

### Two languages
Chinese and English, switchable in Settings. The choice is remembered.

---

## Requirements

| Component | What it is | How to get it |
|---|---|---|
| **Windows 10/11** | — | — |
| **WSL** with container support | Provides `wslc.exe` | `winget install Microsoft.WSL`, or `wsl --update` |

That is the only dependency. The app checks it on the **System** page and, when `winget` is available, can install or update WSL for you. `wslc` ships with WSL, so the app never downloads it separately. Compose is built in, so there is nothing else to install.

---

## Install

### Portable executable

Download `WSLC-Manager-<version>-windows-amd64.zip` from [Releases](https://github.com/ArnoChenFx/wslc-manager/releases), extract it anywhere, and run `WSLC Manager.exe`.

### Installer

Download and run `wslc-manager-setup-<version>-amd64.exe`. It installs the app and creates a Start Menu entry.

### Command-line tool

The Compose engine is also available as a standalone `wslc-compose` command, for terminals and scripts. Download `wslc-compose-<version>-windows-amd64.zip` from [Releases](https://github.com/ArnoChenFx/wslc-manager/releases), extract `wslc-compose.exe` somewhere on your `PATH`, and run it in a directory that has a `compose.yaml`:

```powershell
wslc-compose up -d          # create and start the services
wslc-compose ps             # list the containers
wslc-compose logs -f web    # follow one service's logs
wslc-compose exec web sh    # open a shell in a container
wslc-compose down           # stop and remove everything
```

Commands: `up`, `down`, `start`, `stop`, `restart`, `ps`, `logs`, `exec`, `pull`, `build`, `config`, `version`. Run `wslc-compose <command> --help` for each one's options.

Options that apply to the whole project go **before** the command: `-f/--file` (repeatable), `-p/--project-name`, `--env-file`, `--profile`, and `--dry-run`, which prints the `wslc` commands instead of running them.

```powershell
wslc-compose -f compose.yaml -f override.yaml -p demo up -d
wslc-compose --dry-run up -d
```

It manages the same projects as the app (both use one engine and label their containers the same way) and needs only `wslc`. Progress goes to standard error; `ps`, `config` and `logs` write to standard output, so they pipe cleanly. The exit status is `0` on success, `1` on failure, `2` for a usage error, and for `exec` the status of the command itself. The app does not update the command-line tool: download a newer zip to upgrade it.

### From source

```powershell
git clone https://github.com/ArnoChenFx/wslc-manager.git
cd wslc-manager
go build -o "WSLC Manager.exe" .
go build -o wslc-compose.exe ./cmd/wslc-compose   # the command-line tool
```

Requires Go 1.25 or newer.

---

## Getting started

1. **Start a container** — open the **Compose Projects** page, click **Add project**, and choose a directory containing `compose.yaml`. Select the project and click **Start**.
2. **Or pull an image** — open **Images**, click **Pull image**, and enter a reference such as `nginx:alpine`.
3. **Watch it run** — the **Containers** page updates automatically. Double-click a row for logs.

If something is missing, the **System** page tells you which dependency is absent and offers a one-click install.

---

## Notes and limits

`wslc` is in public preview and has real constraints, all surfaced in the app:

- Only one `wslc` invocation is tolerated at a time. This app serializes all calls for you.
- `pause` / `unpause` and the `unless-stopped` / `on-failure` restart policies are not supported yet.
- A WSL session mounts at most about 15 volumes. Beyond that, `wslc` reports *Too many volumes have been mounted*; reset with `wsl --shutdown`.
- Published ports bind to the Windows loopback address, so test them from Windows.
- Terminals at different elevation levels (normal vs. administrator) have separate `wslc` sessions and cannot see each other's containers.

---

## Built with

- **[Go](https://go.dev)** — single static binary, no runtime to install.
- **[MyGo](https://github.com/egoist/mygo)** — native desktop UI in pure Go. Layout, theming, widgets, native file dialogs and the platform window are all provided by it. No HTML, no JavaScript, no bundled browser.
- **[compose-go](https://github.com/compose-spec/compose-go)** — reads and normalises Compose files (the library behind `docker compose`). The `wslc` orchestration on top of it is this repository's own `internal/compose` package.
- **[cobra](https://github.com/spf13/cobra)** — command-line parsing for the `wslc-compose` tool.

---

## License

MIT
