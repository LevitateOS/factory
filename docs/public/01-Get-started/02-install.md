# Install Factory

Download the Factory archive, check it, install the app for your own account and install the execution services that isolate agents once.

## What you need

- An x86-64 Linux computer with systemd 257 or newer and cgroup v2. Debian 13 and Rocky Linux 10 are the reference systems.
- The GTK 3 and WebKitGTK 4.1 runtime libraries. On Rocky Linux 10 they come from EPEL; the installer checks for them and does not change your package repositories.
- `git`, `bash` and `timeout` in `/usr/bin`.
- A desktop session for the account that will use Factory.

## Download and check

```sh
curl -LO https://github.com/LevitateOS/factory/releases/latest/download/factory-linux-x86_64.tar.gz
curl -LO https://github.com/LevitateOS/factory/releases/latest/download/factory-linux-x86_64.tar.gz.sha256
sha256sum -c factory-linux-x86_64.tar.gz.sha256
```

Continue only when the check reports `OK`.

## Install the app

Extract the archive and run its installer as your own desktop user, not as root:

```sh
tar -xzf factory-linux-x86_64.tar.gz
cd factory-linux-x86_64
./install.sh
```

The installer checks the architecture, the runtime libraries and every file in the bundle against its inventory before it installs anything. It installs under `~/.local` and adds Factory to your application menu. To install somewhere else, pass `--prefix` with an absolute directory you own.

## Install the execution services

Agents run inside an isolated execution boundary made of system services. Install them once for your account. This is the only step that needs root:

```sh
sudo ./factory-runtime/execution/install.sh --user "$USER"
```

It places the helpers in `/usr/libexec`, adds the services for your account under `/etc/systemd/system` and enables them. Factory itself never runs as root.

## Start Factory

Open Factory from your application menu, or run `~/.local/bin/factory`. The [first-run guide](03-first-run.md) opens on the first start.
