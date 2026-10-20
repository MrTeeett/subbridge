# SubBridge documentation

**English** · [Русский](README.ru.md) · [Project overview](../README.md)

## Contents

- [Installation](#installation)
- [Web panel](#web-panel)
- [Subscriptions and HWID](#subscriptions-and-hwid)
- [3x-ui integration](#3x-ui-integration)
- [Console management](#console-management)
- [Configuration and stored data](#configuration-and-stored-data)
- [Export formats](#export-formats)
- [Troubleshooting](#troubleshooting)
- [Development](#development)

## Installation

SubBridge runs from a single binary with an embedded web interface. Go, Node.js,
ADB, and a separate Xray process are not required on the target machine.

| Platform | Release architectures | Service manager |
| --- | --- | --- |
| Linux | `amd64`, `arm64`, `armv7`, `386` | systemd or OpenRC |
| Windows | `amd64`, `arm64` | Task Scheduler |

### Linux: install from an archive

Place the release archive and its `checksums.txt` in the same directory.
The following example uses Linux amd64; choose the archive for your CPU.

```bash
tar -xzf subbridge-v1.1.0-linux-amd64.tar.gz
sudo bash install.sh --archive ./subbridge-v1.1.0-linux-amd64.tar.gz --checksums ./checksums.txt
sudo subbridge credentials
```

The installer verifies SHA-256, installs the binary and console manager, creates
an unprivileged `subbridge` user, and configures the detected init system.
On a new installation it starts the service and enables autostart unless
`--no-autostart` is supplied.

Required download and archive utilities are installed through apt, dnf, yum, apk,
pacman, or zypper when missing. Minimal Alpine installations need Bash first:
`apk add bash`.

### Linux: install from GitHub Releases

Network installation and updates require a published release in the repository.

```bash
curl -fL https://raw.githubusercontent.com/MrTeeett/subbridge/main/install.sh -o install.sh
sudo bash install.sh
```

The default is the latest stable release. To install the rolling development release:

```bash
sudo bash install.sh -dev
```

`-dev` (also `--dev` or `--version dev`) selects the `dev` prerelease directly,
without looking up the latest stable release. Choose either `-dev` or `--version`.
The installer saves the selected channel; console updates keep using it.

| Installer option | Purpose |
| --- | --- |
| `-dev`, `--dev` | Install the rolling `dev` prerelease. |
| `--version v1.1.0` | Install a specific release; the default is the latest release. |
| `--repo owner/name` | Use another GitHub repository. |
| `--archive PATH --checksums PATH` | Install a local archive without downloading a release. |
| `--no-autostart` | Do not enable autostart on a new installation. |
| `--stage-dir /absolute/path` | Extract and initialize in a separate directory without changing system users or services. |

Updates preserve configuration, saved HWIDs, the subscription token, and the cache.
An already stopped service remains stopped, and its autostart setting is preserved.
If an update fails after stopping the service, the installer restores the previous
binary, console manager and service definition, then starts the previous service if
it was running. Concurrent installers are rejected. A failed first installation
keeps configuration and credentials for retrying and removes the incomplete service.
Archive and checksum options must be supplied together; local archives cannot be
combined with a release tag. Local installation does not require curl.

If no stable release exists yet, wait for the first successful Actions build and
install with `-dev`. Download and checksum errors leave an existing service running.

### Windows

Extract the amd64 or arm64 ZIP. Run PowerShell as administrator and install from
an archive, or omit `-Archive` and `-Checksums` to download the latest GitHub release.

```powershell
.\install.ps1 -Archive C:\Downloads\subbridge-v1.1.0-windows-amd64.zip -Checksums C:\Downloads\checksums.txt
& "$env:ProgramData\SubBridge\subbridge.ps1"
```

Files are installed under `%ProgramData%\SubBridge`. The installer restricts directory
ACLs to SYSTEM, administrators, and the installing user. It registers the **SubBridge**
startup task in Task Scheduler; it does not install a Windows Service.

Use `-Version v1.1.0`, `-Repository owner/name`, or `-NoAutostart` as needed.
Use `-Dev` (or `-Version dev`) for the development release. For a network install:

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/MrTeeett/subbridge/main/install.ps1 -OutFile install.ps1
.\install.ps1 -Dev
```

Choose either `-Dev` or `-Version`. Console updates preserve the selected channel.
`-Stage -Destination C:\Temp\SubBridge` tests extraction and setup without registering
a task. Configuration and state survive updates, and a failed update restores
the previous EXE.

### Portable operation

From a directory containing the binary:

```bash
./subbridge -config config.json -setup
./subbridge -config config.json
```

On Windows, use `subbridge.exe`. Initial setup creates missing configuration files
and credentials. A blank source is allowed until you configure it through the web panel.
The console manager controls an installed service; it does not install the service itself.

## Web panel

Default address: **`http://127.0.0.1:8780/admin/`**.

For a Linux installation, read the generated username and password with:

```bash
sudo subbridge credentials
```

The initial credentials are stored in `/etc/subbridge/setup-credentials.txt` with
mode `600`. On Windows the file is in `%ProgramData%\SubBridge\config`.
For a portable setup it is beside `management.json`.

From your own computer, establish an SSH tunnel and open the default address:

```bash
ssh -p 22 -L 8780:127.0.0.1:8780 root@SERVER_IP
```

Replace the SSH port, user, and host for your server. If local port 8780 is occupied,
use `-L 18780:127.0.0.1:8780` and open `http://127.0.0.1:18780/admin/`.

**Panel settings** contains the management IP, port, path, account settings,
and SSH tunnel parameters. The SSH fields generate a command; they do not modify
the server's SSH configuration. For access through a tunnel, keep the management
bind address on loopback.

Management settings are stored separately in `management.json`. Changing the bind
address checks the new listener before applying it. If the IP is unavailable or the
port is occupied, the previous listener remains available. After a successful address
change, reconnect your tunnel and open the new address.

Changing the username or password invalidates existing sessions and removes the initial
credentials file. Passwords are stored as salted PBKDF2-SHA256 hashes. To recover access,
use `subbridge reset-admin` and then `subbridge credentials`.

To enable HTTPS, set `tls_cert` and `tls_key` in `management.json` and restart the service.
Use a free port when switching from HTTP to HTTPS. The service user must be able to read
both files.

English is the default, regardless of the browser locale. The **EN / RU** control
is available before and after login. The selected language
is saved in the browser. Service diagnostics retain their original language.

## Subscriptions and HWID

Open **Subscriptions**, name the source, enter its URL or encrypted import link,
and save it. **Add subscription** creates another source; the selector at the top
switches between sources. Each source has its own request profile, HWID, and cache.
Up to 32 sources are supported. **Remove subscription** removes a source after you
save; its private state remains on disk. Removing the primary source clears its URL,
and you can restore it by entering the URL again.

The refresh interval, export profile, and subscription listener are shared by all sources.
The default refresh interval is 300 seconds.

Leave HWID blank to generate a 16-character uppercase hexadecimal identifier.
The service saves an identity for each source URL and reuses a successful identity
after refreshes and restarts. A manually supplied HWID is never rotated automatically.

**HWID attempts** accepts 1–20, with a default of 3. The count includes the first
request. New candidates are tried after HTTP 403 or a recognized HWID/device-limit
response, with a pause between attempts. Network failures, HTTP 429, and 5xx responses
do not trigger identity rotation. An unsuccessful candidate does not replace a previously
accepted identity or the last good cache.

A generated identifier follows the v2rayTun format, but acceptance depends on the
provider. If registration is closed or the device quota is full, use a working HWID
or reset the binding with the provider. Changing HWID does not remove VPN connection
limits enforced by the upstream server.

The request profile contains `user_agent`, `hwid`, `device_os`, `os_version`,
`device_model`, and `app_version`. The default User-Agent is `v2raytun/android`.
A refresh has a 90-second overall timeout, so a slow source may time out before
using every configured attempt.

To import a previously saved SubBridge profile:

```bash
./subbridge -init-from-bundle saved.subbridge.json -config config.json
```

Import creates a new configuration and does not overwrite an existing file.
The portable bundle contains subscription credentials and device metadata.

### Configuration browser

The **Configs** sidebar section expands into **All subscriptions** and one entry
per source. Browse converted nodes by group, search names, servers, ports or protocols,
and sort the results. Each node has its URI and a copy action; each group has its own
subscription URL and configuration download.

The main subscription URL combines sources and removes exact duplicate URIs.
Each group's URL has its own persistent token and only serves that source.
The combined URL also accepts a `source` query parameter to select one source.
Existing single-source configurations and URLs keep working. Cache files and HWIDs
for additional sources live in `state/sources/<id>/`; the primary source keeps its
existing state location. Sources refresh independently within the refresh deadline,
so an unavailable source retains its previous nodes while other sources can update.

## 3x-ui integration

The integration uses 3x-ui's native client external-subscription API. Your panel
must expose the client endpoints under `/panel/api/clients/`, including `externalLinks`.

1. Open **3x-ui** and enter the panel base URL with its secret prefix, **before `/panel/`**, for example
   `https://panel.example:2053/secret-prefix`.
2. Choose username/password, API token, or environment-based authentication.
3. Click **Connect and load lists**. This checks the draft credentials and reads
   clients and inbounds without saving settings or changing the remote panel.
4. Select clients and inbounds from the dropdowns. Both support search, sorting,
   multiple selection, and selecting filtered results. Inbounds show their name, ID,
   protocol and port. Filtering does not clear hidden selections. Use **Reload lists**
   after changing the panel; saved entries missing from the new list remain marked
   until you remove them. Alternatively, choose **All existing and future clients**.
5. Save the connection and recipients with **Save integration**. Changing recipients
   opens the configuration picker automatically. You can also open it with
   **Choose configurations** in **Configurations to add**. The window
   automatically loads converted nodes. Browse sources, search nodes by name,
   server, port or protocol, sort, select filtered results, or show selected nodes.
   Choose individual configs, all configs from a source, or all available configs.
   Click **Apply selection** to add or remove them in 3x-ui. Automatic synchronization
   refreshes existing entries; adding new configs requires applying a new selection.

Client names and inbound IDs are combined as a union. The default refresh interval
is 60 seconds. Selecting an inbound includes its current and future clients when
you apply a selection. These inbounds identify
recipients; the configs to distribute are chosen separately. To add nodes to a client
named `admin`, select that client. The 3x-ui panel login is a separate account.

The **Node name prefix** field only prepends text to exported node names. It does
not select configs or create Xray inbounds. Automatic updates preserve existing names.

**Send configurations through the API** is the default delivery mode. Selected
nodes are stored as external links in 3x-ui and updated on each sync. It works when
SubBridge runs on your computer and 3x-ui runs on a remote server; an inbound
connection from 3x-ui to SubBridge is not needed. Changing the selection removes
the matching SubBridge-managed nodes, including entries added by another instance.
Unrelated configurations and manual 3x-ui entries are preserved.

**Register a subscription URL** keeps URL-based delivery available. In that mode,
the registered URL must be reachable from the 3x-ui process. `127.0.0.1` refers to
the 3x-ui server, which may be different from the computer running SubBridge.
Switching modes replaces this installation's previous delivery entries.
Save the connection and recipients before applying configurations. The integration
checkbox controls automatic updates. The status below the form shows actual
presence in 3x-ui, check time and connection errors.

The integration has its own persistent subscription URL and only serves the selected
configs. The main SubBridge URL and per-source exports keep their full contents.
An empty selection serves no configs; it never falls back to all nodes. Repeated
URIs are deduplicated. Missing saved selections remain visible and removable in the
picker. Individual selections use the node name and transport settings, so named
nodes keep their selection when the provider changes their IP or credentials.
Identical names and transports are distinguished by endpoint. Existing URI-based
selections are migrated against the cached nodes.
Changing the export profile preserves the selection. After saving the connection
and recipients, **Apply selection** saves the checked configurations and starts
a one-time synchronization. Unchecked configurations are removed from the selected
clients, including when automatic synchronization is disabled. **Cancel** discards
the draft. Checked configs present for only some recipients are added to the others
when you apply, without toggling their checkboxes again. Saving recipients or
cancelling the picker does not distribute configs. Other unchanged configs are
preserved, including nodes added elsewhere while the picker was open. Other connection and recipient changes still
use **Save integration**.

### Multiple instances

In API delivery mode, several SubBridge instances can use the same 3x-ui clients
without connecting to each other. Update each instance to this version. The 3x-ui
API is the shared source of truth: both the picker and configuration browser show
whether each node is added, absent, disabled, or present for only some recipients.
Observations are cached for up to eight seconds; **Check 3x-ui** forces a new read.
A failed read shows an unknown state and blocks applying an unverified selection.

Identical connections are deduplicated independently of node names and URI query
ordering. Nodes from the same source retain a shared identity when their IP or
credentials change. Existing import markers migrate without changing connection
parameters; source timestamps prevent stale instances from overwriting newer
connection values. Manual 3x-ui entries remain under manual control.

Automatic synchronization refreshes existing entries. It never adds a missing
node back after another instance or 3x-ui removes it. Use **Apply selection** to add
nodes, including new provider nodes. Simply connecting a new instance does not
replace the remote selection. Subscription URL delivery remains a single-instance
mode and does not expose the same shared presence checks.

For environment-based authentication, the default variables are
`SUBBRIDGE_PANEL_TOKEN`, `SUBBRIDGE_PANEL_USERNAME`, and `SUBBRIDGE_PANEL_PASSWORD`.
The token takes priority in that mode. See `deploy/panel.env.example`; systemd loads
`/etc/subbridge/panel.env`. For 2FA-enabled accounts, use an API token.
Stored password and token values are not returned to the web form; leaving a secret
field blank keeps its existing value.

For URL delivery, in the subscription's advanced settings, configure `listen` and `public_url` so
that the resulting URL is reachable **from the 3x-ui process**. When both services
run on the same host, `127.0.0.1:8770` is suitable. A panel in Docker has its own
loopback interface; use an address it can reach and a corresponding listener.

SubBridge manages its external entries while preserving existing unrelated entries,
client UUIDs, quotas, expiry settings, and inbound configuration. Repeated syncs do not
create duplicates or re-enable an entry disabled by hand. Existing SubBridge entries
at the previous combined URL migrate to the dedicated integration URL. Removing a client from the
selection does not delete a previously added entry. After changing `public_url`,
remove the old entry in 3x-ui manually.

External nodes appear in the user's subscription. They do not become local Xray
inbounds, and traffic goes directly to the provider. 3x-ui's local traffic limits do
not control those external credentials. Raw/Base64 output preserves extended URI
parameters; JSON and Clash output additionally depend on 3x-ui's own importer.

3x-ui replaces the complete external-link list on update. SubBridge rereads it before
writing, but simultaneous manual edits can still conflict. The panel may also cache
external subscriptions, delaying visible updates.

## Console management

On Linux, run `sudo subbridge` for the English console menu. Every action has its own
row; **`0. Exit`** closes the menu. A failed command or cancelled uninstall returns
to the menu. Service actions require an installed service.

| Command | Action |
| --- | --- |
| `subbridge start` | Start the service. |
| `subbridge stop` | Stop the service. |
| `subbridge restart` | Restart the service. |
| `subbridge status` | Show service status. |
| `subbridge logs` | Show the last 100 log lines. |
| `subbridge update` | Update from the saved stable or dev channel. |
| `subbridge update -dev` | Install or switch to the rolling development release. |
| `subbridge update --version latest` | Install or switch to the latest stable release. |
| `subbridge update --version v1.1.0` | Install a specified release. |
| `subbridge enable` | Enable autostart. |
| `subbridge disable` | Disable autostart without stopping the current service. |
| `subbridge settings` | Change management IP, port, or username and restart. |
| `subbridge reset-admin` | Generate a new management password and restart. |
| `subbridge credentials` | Show initial or reset credentials. |
| `subbridge management` | Show management settings without the password hash. |
| `subbridge url` | Print the private subscription URL. |
| `subbridge check` | Validate configuration without network requests. |
| `subbridge uninstall` | Remove binaries and service definitions, keeping config and state. |

Administrative commands require root. Uninstall asks for confirmation unless
`--yes` is supplied. To remove private configuration and state completely, delete
the corresponding directories separately after stopping the service.

On Windows, open `subbridge.ps1` for its menu or pass one command, for example:

```powershell
& "$env:ProgramData\SubBridge\subbridge.ps1" -Command restart
```

Use `-Command update -Dev` to switch to dev, or `-Command update -Version latest`
to return to stable. `-Command update` without a version keeps the saved channel.

Available commands include `start`, `stop`, `restart`, `status`, `logs`, `update`,
`enable`, `disable`, `settings`, `reset-admin`, `credentials`, `management`, `url`,
`check`, and `uninstall`. Task operations need administrator privileges.

## Configuration and stored data

Source, export, and 3x-ui settings live in `config.json`. Panel access and account
settings live in a separate `management.json`.
See the [configuration example](../deploy/config.example.json).

| Data | Linux installation | Windows installation |
| --- | --- | --- |
| Main configuration | `/etc/subbridge/config.json` | `%ProgramData%\SubBridge\config\config.json` |
| Management configuration | `/etc/subbridge/management.json` | `%ProgramData%\SubBridge\config\management.json` |
| Initial credentials | `/etc/subbridge/setup-credentials.txt` | `%ProgramData%\SubBridge\config\setup-credentials.txt` |
| Persistent state | `/var/lib/subbridge` | `%ProgramData%\SubBridge\state` |
| Core binary | `/usr/local/lib/subbridge/subbridge-core` | `%ProgramData%\SubBridge\subbridge.exe` |
| Console manager | `/usr/local/bin/subbridge` | `%ProgramData%\SubBridge\subbridge.ps1` |

State contains the subscription `token`, `cache.json`, and `hwid.json`. Files are saved
through a temporary file followed by a rename. Keeping the token preserves the subscription URL.
A source failure retains the last good cache, including across restarts. Changing
the source URL or request profile prevents reusing a cache from the previous source.

The installed binary directory also contains `repository` and `release-channel`,
which the console manager uses for updates. Existing installations without a saved
channel use stable by default.

On Linux, secret files must have mode `600` and the state directory mode `700`.
On Windows, protect them with ACLs; POSIX permission checks do not apply. The installers
set these permissions automatically. A custom state directory is configured through
the file and a service restart rather than the web form.

### Core command-line flags

Use these with the standalone binary, or use `subbridge run` with the installed Linux
manager to pass flags through to the core.

| Flag | Action |
| --- | --- |
| `-config PATH` | Choose the main configuration file. |
| `-management-config PATH` | Choose the separate management file. |
| `-setup` | Create missing configuration and credentials. |
| `-no-web` | Disable web management for this run. |
| `-admin-ip IP`, `-admin-port PORT`, `-admin-username NAME` | Save management settings and exit. |
| `-reset-admin` | Save a generated management password and exit. |
| `-admin-password-env NAME` | Read a new password from the named environment variable. |
| `-check` | Validate configuration and embedded keys without network access. |
| `-dry-run` | Fetch and cache the source, check client selection, and skip 3x-ui writes. |
| `-once` | Refresh and synchronize once, then exit. |
| `-print-url` | Print the private subscription URL and exit. |
| `-log-file PATH` | Append service diagnostics to a private file. |

After changing management files with CLI flags, restart an already running service.
Keep the daemon running to serve its subscription URL. Normal service logs omit source
URLs, device identifiers, client UUIDs, and panel secrets.

## Export formats

The private `/sub/<token>` endpoint returns standard URIs. The web panel also lets
you download every configuration or only VLESS entries.

| Query parameter | Values |
| --- | --- |
| `format` | `raw` (default), `base64` |
| `profile` | `original`, `v2rayn` |
| `protocol` | `all` (default), `vless` |
| `source` | Source ID, or omitted to combine all sources. The primary source ID is `default`. |

`original` preserves existing URIs byte for byte, including `pqv`, `extra`,
`encryption`, unknown parameters, and repeated query fields. `v2rayn` changes only
`fp=qq` to `firefox` for VLESS REALITY; it does not alter the source cache.
Some mobile connections need the original fingerprint, so `original` is the default.

Supported inputs include Xray JSON/JSONC, URI lists, Base64, and encrypted v2rayTun
and Happ links. Exports cover VLESS, VMess, Trojan, Shadowsocks, SOCKS/HTTP,
and Hysteria 2. DNS, routing, chains, and some JSON-specific transport settings cannot
be represented by individual URIs; the panel reports conversion notes.

Limits: 32 sources, 8 MiB and up to 10,000 nodes per source, and a 2 MiB combined output limit when the 3x-ui
integration is enabled. Provider responses containing placeholder local addresses
do not replace a valid cache.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `Unit subbridge.service could not be found` | Install the service with `install.sh`. Running the console script alone does not create it. |
| The panel is unreachable | Check bind IP, port, panel path, and both ends of the SSH tunnel. On the server, run `subbridge status` and `subbridge logs`. |
| A new management address is rejected | Verify that the server owns the IP and that the port is free. The previous listener stays available after a failed change. |
| HWID attempts are exhausted | Enter a known working HWID or check the provider's device binding and quota. |
| The source fails but old nodes remain | The last good cache is being served. Check the error and last successful refresh in Overview. |
| Nodes do not appear in 3x-ui | Check authentication, client selection, the base URL prefix, external-link API support, and whether 3x-ui can reach `public_url`. |
| A desktop client fails with `fp=qq` | Try the `v2rayn` export profile; keep the original profile available for mobile clients. |
| A configuration file is rejected on Linux | Check mode `600`, state directory mode `700`, and access for the service user. |

## Development

Use **Go 1.26.x**. Runtime dependencies are limited to the standard library; the UI
is embedded and requires no Node.js or frontend build. Local build scripts require
Linux, cgroup v2, and systemd. Race tests also require a C compiler.

From the source directory:

```bash
./test.sh
./build.sh all
./package.sh
```

The scripts apply and verify a hard OS-level limit of at most 4 GiB for the complete
process tree, disable swap for the scope, and build with one worker. They also set
`GOMAXPROCS=1`, `GOMEMLIMIT=2GiB`, `GOFLAGS=-p=1`, and `GOTOOLCHAIN=local`.
If the cgroup limit cannot be established and verified, compilation does not start.
The Go heap setting is supplementary to the OS limit.

`test.sh` runs `go vet` and race tests. `build.sh` creates static binaries with CGO
disabled. To build one target, use `./build.sh windows-amd64` or another release target.
Set `SUBBRIDGE_VERSION=1.1.0` to choose the embedded and packaged version.
For a development build, use a version such as `1.1.0-dev.local` and set
`SUBBRIDGE_RELEASE_TAG=dev` when packaging. Dev archive names remain
`subbridge-dev-<platform>-<architecture>` while the binary retains its full version.

`dist/releases` contains Linux tar.gz packages, Windows ZIP packages, a source archive,
and `checksums.txt`. Documentation in both languages is included in release archives.
The packager is written in Go and uses explicit file lists for release contents.

The workflow in `.github/workflows/release.yml` runs on pushes, pull requests,
and manual dispatch. It builds targets sequentially, checks Linux binaries on Ubuntu
22.04/24.04 and Alpine, and defines native Windows tests on Server 2022/2025.
Windows builds and tests run inside a verified 4 GiB Job created by a supervisor
cross-compiled in the limited Linux job.

Each push to any branch builds a development version containing the commit SHA and,
after all checks pass, creates or replaces the `dev` prerelease. Its Git tag moves to
the built commit. A newer branch push cancels an unfinished dev build. Pull requests
produce build artifacts without publishing a release.

Pushing a version tag such as `v1.1.0` publishes that version after its checks pass.
The `dev` prerelease is never marked as the latest stable release. The workflow uses
GitHub's built-in `GITHUB_TOKEN`; no separate bot account is needed.

Tests cover decryption, protocol conversion, request headers, limits, HWID retries,
cache retention, cancellation, concurrent access, web authentication, CSRF,
listener rollback, and the 3x-ui API contract. `scripts/check-install.sh` checks staged
installation, updates, checksum rejection, and removal without changing host services.
`scripts/check-release.sh` checks stable/dev downloads, saved channels, and release
publication against offline curl and GitHub CLI fixtures.
`scripts/check-selection.mjs` checks selection changes for multiple recipients,
partial presence and remote removals with Node.js 22 or later (tests only).
`scripts/check-install-services.sh` checks service-account permissions, startup
failures, full installer rollback, locking and service-state preservation against
simulated systemd/OpenRC commands in disposable Docker containers.
Integration tests use a local panel fixture; check your provider and panel when deploying.
