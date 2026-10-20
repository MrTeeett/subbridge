# SubBridge

**English** · [Русский](README.ru.md)

A server-side converter for encrypted subscriptions.

SubBridge fetches a subscription using a configurable v2rayTun device profile,
decodes its contents, and serves standard proxy links at a persistent URL.
Use the result directly in a client or add it to selected 3x-ui user subscriptions.
The service runs as a single Go binary with an embedded web interface.

## Features

- **Multiple sources** — up to 32 subscriptions with separate caches and HWIDs,
  a combined export, and a configuration browser grouped by subscription.
- **Subscription conversion** — v2rayTun encrypted links, Happ links, Xray JSON/JSONC,
  plain URI lists, and Base64 subscriptions.
- **Client exports** — VLESS, VMess, Trojan, Shadowsocks, SOCKS/HTTP, and Hysteria 2;
  download all configurations or VLESS only.
- **Device profiles** — a saved automatic HWID or a manually supplied identifier,
  configurable request headers, and bounded HWID retries.
- **3x-ui integration** — send selected configs through the API or register a subscription URL while
  preserving existing external links and client settings. Searchable, sortable
  client and inbound selectors load directly from the connected panel. Choose
  individual configs or entire source subscriptions in a dedicated picker.
- **Web administration** — English and Russian, separate management settings,
  configurable bind address, and SSH tunnel commands.
- **Service management** — Linux and Windows installers, console menus, updates,
  autostart controls, and a cache that survives source failures and restarts.

## Quick start

On Linux, download and run the installer. It selects the archive for your CPU
and verifies its checksum:

```bash
curl -fL https://raw.githubusercontent.com/MrTeeett/subbridge/main/install.sh -o install.sh
sudo bash install.sh
sudo subbridge credentials
```

To install the development build from the rolling `dev` release, use
`sudo bash install.sh -dev`. Subsequent console updates stay on the selected channel.
For a newly created repository with no stable release yet, use `-dev` after the
first successful [Actions build](https://github.com/MrTeeett/subbridge/actions).

The web panel listens at `http://127.0.0.1:8780/admin/` by default.
Connect through an SSH tunnel, sign in, and add your source subscription.
See [first connection](docs/README.md#web-panel) for the tunnel command,
or [installation](docs/README.md#installation) for Windows and other options.

Running `subbridge` opens the console menu after installation.

## Documentation

| Language | Project overview | User and deployment guide |
| --- | --- | --- |
| English | [README](README.md) | [Documentation](docs/README.md) |
| Русский | [О проекте](README.ru.md) | [Документация](docs/README.ru.md) |

The guides cover installation, panel access, HWID profiles, 3x-ui integration,
configuration files, exports, troubleshooting, and development.

## Supported platforms

| Platform | Architectures | Service management |
| --- | --- | --- |
| Linux | `amd64`, `arm64`, `armv7`, `386` | systemd or OpenRC |
| Windows | `amd64`, `arm64` | Task Scheduler |

Linux binaries are statically linked. The target machine does not need Go,
Node.js, a connected phone, or a separate Xray installation to run the converter.
The 3x-ui integration requires its native client external-subscription API.
External nodes connect directly to their provider; they do not become local Xray inbounds.

## Development

Built with **Go 1.26** and the standard library, with no frontend build step.
Source builds, tests, and release packaging use the included scripts, which enforce
an OS-level memory limit of 4 GiB and one build worker.
See [development](docs/README.md#development) for commands and GitHub Actions releases.

## License

SubBridge is licensed under the [GNU General Public License v3.0](LICENSE)
(`GPL-3.0-only`). Third-party components retain their own licenses;
see `licenses/` and `ui/OFL.txt` in the source archive.
