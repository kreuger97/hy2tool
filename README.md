# hy2tool

Hysteria 2 server installer and manager.

## Install

Run these steps in a root shell on a Linux server with systemd. The release
below is for x86-64 (amd64). Before changing the server, the installer checks
for `bash`, `curl`, `openssl`, `chmod`, `systemctl` and `journalctl`. Missing
required commands stop installation with a complete list of dependencies to
install. `wget` is an alternative for downloading the manager; the installer
itself still requires `curl`.

`ufw` is optional. If available, the installer adds inbound rules for the
Hysteria UDP port and enabled masquerade TCP ports. If absent, firewall setup
is reported as skipped with a manual configuration reminder and the exact
ports to allow. Configure your firewall/security group yourself in that case;
for the default ports with masquerade enabled: UDP 443, TCP 80 and TCP 443.

1. Download the manager using **one** of these commands.

   With curl:

   ```bash
   curl -fsSL -o hy2tool https://github.com/kreuger97/hy2tool/releases/latest/download/hy2tool_linux_amd64
   ```

   Or with wget:

   ```bash
   wget -O hy2tool https://github.com/kreuger97/hy2tool/releases/latest/download/hy2tool_linux_amd64
   ```

2. Make the downloaded file executable.

   ```bash
   chmod +x hy2tool
   ```

3. Start the interactive installer.

   ```bash
   ./hy2tool install
   ```

4. Check the service after installation.

   ```bash
   ./hy2tool status
   ```

5. If the service fails, inspect its recent logs.

   ```bash
   ./hy2tool logs -n 40
   ```

The installer reports completed, failed and skipped stages. Failed commands
include their exit code, stdout and stderr; service failures also include
systemd status and the last 40 journal records. The full report remains in the
terminal after closing the TUI. A missing systemd unit is created automatically;
existing units and masks are preserved. The upstream installer script is downloaded
to a temporary file over HTTPS and runs only after a successful, non-empty
download. The temporary file is removed on success and failure.

## Reinstall

Running `./hy2tool install` again detects a previous installation from the
Hysteria executable, server configuration, certificates or systemd unit.
Before asking for new settings, it warns that full reinstallation will stop
the server and replace the executable, configuration, certificates and main
systemd unit. Only an explicit `y` authorizes this; Enter defaults to refusal
and exits without changes.

After approval, dependencies are checked before stopping the old service.
The upstream installer is run with `--force`, new configuration and certificates
are generated, the main systemd unit is recreated, and the service is restarted
so the new settings take effect. Existing clients may need a new password,
port or certificate settings. Systemd drop-in overrides are retained.
To update the executable while keeping your settings, use `./hy2tool update`.

## Commands

| Command | Description |
|---------|-------------|
| `hy2tool install` | Interactive installer |
| `hy2tool update` | Update Hysteria 2, preserving configuration |
| `hy2tool link` | Show connection link + QR code |
| `hy2tool logs [-n N] [-f]` | Show recent service journal records or follow live |
| `hy2tool status` | Service status |
| `hy2tool start` | Start service |
| `hy2tool stop` | Stop service |
| `hy2tool restart` | Restart service |

## Features

- Self-signed SSL certificate
- Masquerade proxy (optional)
- Bandwidth limits
- Optional firewall configuration through ufw, with manual setup guidance when unavailable
- Automatic creation of a missing `hysteria-server.service` (existing units and masks are preserved)
- Random password generation

## Update server

```bash
./hy2tool update
```

Downloads the latest Hysteria 2 binary for the current Linux architecture from
`download.hysteria.network`. Configuration, certificates, systemd units and
firewall rules are left untouched. A running service is restarted and checked;
a stopped service remains stopped. If restarting the updated binary fails, the
previous binary is restored and the service restart is retried. Download or
validation failures leave the installed binary in place. This updates Hysteria,
not the `hy2tool` manager itself.

## Service journal

```bash
./hy2tool logs              # Last 40 records
./hy2tool logs -n 100       # Last 100 records
./hy2tool logs -n 100 -f    # Last 100 records, then follow live
./hy2tool logs -n 0 -f      # Only new records
./hy2tool logs --help
```

`--lines` and `--follow` are aliases for `-n` and `-f`. Press Ctrl+C to stop
following. Output comes directly from `journalctl` for `hysteria-server.service`,
without a pager. Logs can also be viewed when Hysteria is stopped or its binary
has been removed.

## Build and validate

Use Go 1.27.3 or newer. From the repository root:

```bash
go mod download
```

```bash
go test -race ./...
```

```bash
go vet ./...
```

Build the Linux x86-64 manager:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o hy2tool ./cmd/hy2tool
```

Tests use temporary directories and stub service commands; they do not install
Hysteria or modify the host's `/etc`. Real service startup, permissions and live
journal viewing should also be checked on a Linux server with systemd. The
included Docker Compose setup provides a privileged Debian 12 test container.
